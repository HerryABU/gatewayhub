package proxy

import (
	"log"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/models"
	"gatewayhub/internal/stats"
)

// Manager 反向代理管理器（内存路由表 + 热加载 + 子域名/路径双模式）
type Manager struct {
	db         *gorm.DB
	stats      *stats.Writer
	fileLog    *accesslog.FileLogger // 访问日志落盘（含请求头），可为 nil
	baseDomain string                // 子域名后缀（如 localhost），空则禁用子域名路由
	mu         sync.RWMutex
	m          map[string]*Entry
	multi      []string // 含 "/" 的多级前缀，按长度降序（最长前缀匹配用）
	warnSig    string   // 上一次「路由被跳过」告警的指纹，用于去重（同一批错误不重复刷屏）
}

// NewManager 创建代理管理器
func NewManager(db *gorm.DB, stats *stats.Writer, baseDomain string) *Manager {
	return &Manager{db: db, stats: stats, baseDomain: baseDomain, m: make(map[string]*Entry)}
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
	for _, r := range routes {
		e, err := m.buildEntry(r)
		if err != nil {
			log.Printf("[proxy] 路由 %s 已跳过（target=%q）：%v", r.Prefix, r.Target, err)
			skipped = append(skipped, r.Prefix)
			continue
		}
		m.m[r.Prefix] = e
	}
	m.rebuildMultiLocked()
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

// Upsert 新增或更新路由（写库 + 热加载）
func (m *Manager) Upsert(r models.Route) error {
	e, err := m.buildEntry(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.m[r.Prefix] = e
	m.rebuildMultiLocked()
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
