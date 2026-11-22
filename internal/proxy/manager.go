package proxy

import (
	"log"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/hopguard"
	"gatewayhub/internal/models"
	"gatewayhub/internal/stats"
)

// SelfInfo 描述网关自身，用于识别「自指」路由并限制链式转发跳数。
//
// 自指是合法配置（把控制台/另一个前缀的站点挂到新前缀下、迁移期做旧地址别名），
// 所以这里只做识别与告警，真正的防死循环靠 hopguard 的跳数上限。
type SelfInfo struct {
	Port    int // 网关监听端口；0 表示不做自指识别
	MaxHops int // 链式转发最大跳数；0 用 hopguard 默认值
}

// Manager 反向代理管理器（内存路由表 + 热加载 + 子域名/路径双模式）
type Manager struct {
	db         *gorm.DB
	stats      *stats.Writer
	fileLog    *accesslog.FileLogger // 访问日志落盘（含请求头），可为 nil
	baseDomain string                // 子域名后缀（如 localhost），空则禁用子域名路由
	self       SelfInfo              // 自身地址信息（自指识别 + 跳数上限）
	hops       *hopguard.Guard
	mu         sync.RWMutex
	m          map[string]*Entry
	multi      []string // 含 "/" 的多级前缀，按长度降序（最长前缀匹配用）
	warnSig    string   // 上一次「路由被跳过」告警的指纹，用于去重（同一批错误不重复刷屏）
	selfSig    string   // 上一次「自指路由」告警的指纹（同样的批次不重复刷屏）
}

// NewManager 创建代理管理器
func NewManager(db *gorm.DB, stats *stats.Writer, baseDomain string, self SelfInfo) *Manager {
	return &Manager{
		db:         db,
		stats:      stats,
		baseDomain: baseDomain,
		self:       self,
		hops:       hopguard.New(self.MaxHops),
		m:          make(map[string]*Entry),
	}
}

// SetFileLogger 注入访问日志落盘器（含请求头，按天/小时）
func (m *Manager) SetFileLogger(l *accesslog.FileLogger) {
	m.fileLog = l
}

// Load 从数据库加载全部路由到内存。
//
// 单条路由解析失败（target 写错、环境变量缺失等）不会中断整体加载，但**必须留痕**：
// 历史实现是 `if err == nil { ... }` 静默跳过，结果表现为「后台配了路由、内存表里没有、
// 请求永远回落控制台 SPA」，日志里只少了一条、没有任何错误提示（实测 target 写成
// `127.0.0.1:9100` 时就是这样，正解是 `:9100` 或 `http://127.0.0.1:9100`）。
func (m *Manager) Load() error {
	var routes []models.Route
	if err := m.db.Find(&routes).Error; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m = make(map[string]*Entry, len(routes))
	var skipped []string
	selfs := make([]string, 0, len(routes))
	for _, r := range routes {
		e, err := m.buildEntry(r)
		if err != nil {
			log.Printf("[proxy] 路由 %s 已跳过（target=%q）：%v", r.Prefix, r.Target, err)
			skipped = append(skipped, r.Prefix)
			continue
		}
		m.m[r.Prefix] = e
		if e.Self {
			selfs = append(selfs, r.Prefix)
		}
	}
	m.rebuildMultiLocked()
	m.warnSelfLocked(selfs, len(routes))
	if len(skipped) > 0 {
		// 去重：启动期 Load 可能被调用两次（加载 + 演示数据补齐后重载），
		// 同一批错误只提示一次，免得用户以为出了两个问题。
		sig := strings.Join(skipped, ",")
		if sig != m.warnSig {
			m.warnSig = sig
			log.Printf("[proxy] 警告：%d/%d 条路由因 target 无法解析而未生效（%s）——"+
				"target 只接受 `:端口` 或完整 URL（如 http://host:port/base），"+
				"写 `host:port` 会被拒绝；请在控制台「路由」里修正后保存",
				len(skipped), len(routes), sig)
		}
	} else {
		m.warnSig = ""
	}
	return nil
}

// warnSelfLocked 对「自指路由」打一次汇总告警（调用方需持有写锁）。
//
// 自指不是错误：把控制台、另一个前缀的站点挂到新前缀下时，target 本来就该指向自己。
// 但完全不说会有两种灾难：一是配错了（target 根路径恰好等于自身前缀，永远绕回来），
// 二是链路来回转几十圈才被跳数上限拦住，日志里只有一串 508 看不出是哪个前缀。
func (m *Manager) warnSelfLocked(selfs []string, total int) {
	sort.Strings(selfs)
	sig := strings.Join(selfs, ",")
	if sig == m.selfSig {
		return // 同一批自指路由只提示一次（启动期 Load 会被调用两次）
	}
	m.selfSig = sig
	if len(selfs) == 0 {
		return
	}
	var dead []string
	for _, p := range selfs {
		if e, ok := m.m[p]; ok && selfLoopsBack(e) {
			dead = append(dead, p)
		}
	}
	log.Printf("[proxy] 检测到 %d/%d 条自指路由（target 指向网关自身 :%d）：%s；"+
		"链式跳数上限 %d，超过即返回 508", len(selfs), total, m.self.Port, sig, m.hops.Max())
	if len(dead) > 0 {
		log.Printf("[proxy] 警告：自指路由 %s 的 target 根路径等于自身前缀，转发会绕回同一前缀（必然死循环）；"+
			"请把 target 指向另一个前缀（如 http://127.0.0.1:%d/other）",
			strings.Join(dead, ", "), m.self.Port)
	}
}

// selfLoopsBack 判断自指路由是否「绕回自己」：target 根路径去斜杠后等于自身前缀。
func selfLoopsBack(e *Entry) bool {
	root := strings.Trim(strings.TrimSuffix(e.Parsed.Root, "/"), "/")
	if root == "" {
		return false
	}
	return root == strings.Trim(e.Route.Prefix, "/")
}

// selfPrefixesLocked 返回当前内存表中的自指前缀（调用方需持有读锁或写锁）。
func (m *Manager) selfPrefixesLocked() []string {
	out := make([]string, 0)
	for p, e := range m.m {
		if e.Self {
			out = append(out, p)
		}
	}
	return out
}

// Upsert 新增或更新路由（写库 + 热加载）
func (m *Manager) Upsert(r models.Route) error {
	e, err := m.buildEntry(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.m[r.Prefix] = e
	m.rebuildMultiLocked()
	m.warnSelfLocked(m.selfPrefixesLocked(), len(m.m))
	m.mu.Unlock()
	return nil
}

// Remove 从内存路由表删除
func (m *Manager) Remove(prefix string) {
	m.mu.Lock()
	delete(m.m, prefix)
	m.rebuildMultiLocked()
	m.mu.Unlock()
}

// Get 查找路由
func (m *Manager) Get(prefix string) (*Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.m[prefix]
	return e, ok
}

// Exists 判断前缀是否已存在
func (m *Manager) Exists(prefix string) bool {
	_, ok := m.Get(prefix)
	return ok
}

// HasPathPrefix 判断某「首段」是否为已注册业务路由（含多级前缀的首段，如 v2/beta 的 v2）。
// 供子路径部署前缀剥离逻辑使用：首段命中业务路由时不得剥离。
func (m *Manager) HasPathPrefix(first string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.m[first]; ok {
		return true
	}
	for _, p := range m.multi {
		if strings.HasPrefix(p, first+"/") {
			return true
		}
	}
	return false
}

// Count 返回路由数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.m)
}

// rebuildMultiLocked 重建「含 / 的多级前缀」降序列表（调用方需持有写锁）
func (m *Manager) rebuildMultiLocked() {
	multi := make([]string, 0)
	for p := range m.m {
		if strings.Contains(p, "/") {
			multi = append(multi, p)
		}
	}
	sort.Slice(multi, func(i, j int) bool { return len(multi[i]) > len(multi[j]) })
	m.multi = multi
}

// Matchable 判断请求是否命中任何路由（子域名或路径），供 NoRoute 决定转发 or SPA 回退。
func (m *Manager) Matchable(path, host string) bool {
	if m.MatchableHost(host) {
		return true
	}
	_, _, ok := m.matchPath(path)
	return ok
}

// MatchableHost 判断 Host 是否指向一个启用的子域名路由。
//
// 子域名模式下「路径就是后端根路径」，后端自己的 /api、/assets 与网关同名路径天然重叠，
// 所以 NoRoute 必须在网关自身的 /api 守卫与 SPA 静态资源之前先做这个判断，
// 否则后端 API 会被网关吞成 404（实测 openlist 子域名模式 5 个 /api/public/* 全 404）。
func (m *Manager) MatchableHost(host string) bool {
	prefix, ok := m.matchSubdomain(host)
	if !ok {
		return false
	}
	e, ok := m.Get(prefix)
	return ok && e.Route.Status == models.StatusActive
}
