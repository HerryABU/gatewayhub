// Package engine 是路径隔离网关的「循环执行器」：每个请求都走一遍
// 拦截 → 路径/头/Cookie 修正 → 转发 → 响应修正，处理完即结束（无跨请求状态）。
//
// 设计约束（对应需求 NFR）：
//   - 无状态：所有请求级信息放在 context 里的 requestState，不落全局变量；
//   - 确定性：改写规则是纯函数（forward/cookie/respond 包），模式热切换只影响后续请求；
//   - 可并发：热路径只有原子计数与读指针，服务之间互不阻塞；
//   - 隔离：单个服务异常（超时/熔断）只影响自身，其他服务路由不受影响。
package engine

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/hopguard"
	"gatewayhub/internal/isolation/breaker"
	"gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/diag"
	"gatewayhub/internal/isolation/forward"
	"gatewayhub/internal/isolation/matcher"
	"gatewayhub/internal/isolation/metrics"
	"gatewayhub/internal/proxy/rewrite"
	"gatewayhub/internal/target"
)

// Options 是引擎依赖注入项（全部可空，缺省时内部自建）。
type Options struct {
	Metrics *metrics.Registry
	Audit   *diag.Audit
	Loop    *metrics.LoopDetector
	Table   *config.Table
	// Logger 访问日志落盘器（与普通反代共用同一个写入器，落同一套目录结构）。
	// 为 nil 时隔离链路不写访问日志。
	Logger *accesslog.FileLogger
	// TrustProxies 表示前面是否还有可信反代（决定客户端 IP 是否取 XFF/X-Real-IP）。
	TrustProxies bool
	// SelfPort 是网关自身监听端口：用于识别「target 指向自己」的自指服务并告警。
	// 0 表示不识别（仍受跳数上限保护）。
	SelfPort int
	// MaxHops 是链式转发最大跳数（自指 + 嵌套），0 用 hopguard 默认值。
	MaxHops int
}

// Engine 是隔离网关实例。
type Engine struct {
	mu       sync.RWMutex
	matcher  *matcher.Table
	runtimes map[string]*runtime // key: 小写服务名
	table    *config.Table

	metrics *metrics.Registry
	audit   *diag.Audit
	loop    *metrics.LoopDetector
	pool    *transportPool
	logger  *accesslog.FileLogger
	// hops 限制「服务 target 指向网关自身」时的链式转发深度（自指/嵌套）。
	hops *hopguard.Guard
	// selfPort 是网关自身端口，仅用于自指服务告警；0 表示不识别。
	selfPort int

	loopInjected bool
	// trustProxies 决定客户端 IP 是否取 XFF/X-Real-IP（前面有可信反代时才可开）
	trustProxies bool

	// guard 识别「网关与后端互相推回去」的确定性重定向自环（跨服务共享，按 key 区分）。
	guard *selfLoopGuard
	// absHints 对「越界归因」日志做节流：同一 服务+前缀 60 秒只打一条，
	// 否则一个没配 base path 的 SPA 能把日志刷爆。
	absHints sync.Map // key: 服务名|前缀 → lastUnix(int64)
}

// New 创建引擎；table 为 nil 时创建一个空表（此时引擎不匹配任何路径，等价于未启用）。
func New(opts Options) *Engine {
	e := &Engine{
		runtimes:     make(map[string]*runtime),
		metrics:      opts.Metrics,
		audit:        opts.Audit,
		pool:         newTransportPool(),
		logger:       opts.Logger,
		trustProxies: opts.TrustProxies,
		guard:        newSelfLoopGuard(selfLoopWindow),
		hops:         hopguard.New(opts.MaxHops),
		selfPort:     opts.SelfPort,
	}
	if e.metrics == nil {
		e.metrics = metrics.NewRegistry()
	}
	if e.audit == nil {
		e.audit = diag.NewAudit("")
	}
	if opts.Loop != nil {
		e.loop = opts.Loop
		e.loopInjected = true
	}
	tbl := opts.Table
	if tbl == nil {
		tbl = &config.Table{}
	}
	e.apply(tbl)
	return e
}

// apply 原子替换配置快照与运行时表（热更新路径）。
func (e *Engine) apply(tbl *config.Table) {
	runtimes := make(map[string]*runtime, len(tbl.Services))
	for _, svc := range tbl.Services {
		runtimes[strings.ToLower(svc.Name)] = newRuntime(svc, e.pool, e.metrics, tbl.Breaker, e.guard)
	}
	m := matcher.New(tbl.Services)

	e.mu.Lock()
	e.matcher = m
	e.runtimes = runtimes
	e.table = tbl
	if !e.loopInjected {
		e.loop = metrics.NewLoopDetector(tbl.Loop.Threshold, time.Duration(tbl.Loop.Window)*time.Second)
	}
	selfPort := e.selfPort
	maxHops := e.hops.Max()
	e.mu.Unlock()

	e.warnSelf(tbl, selfPort, maxHops)
}

// warnSelf 对「target 指向网关自身」的服务告警。
//
// 自指是合法用法（把控制台、另一个前缀的站点挂到新前缀下；迁移期做旧地址别名），
// 但必须让运维知道两件事：这条链路会再进一次网关（所以嵌套的前缀必须存在，
// 否则会 404），以及来回绕的深度由跳数上限兜底。
func (e *Engine) warnSelf(tbl *config.Table, selfPort, maxHops int) {
	if selfPort <= 0 || tbl == nil {
		return
	}
	var selfs []string
	for _, svc := range tbl.Services {
		if svc.Enabled && target.IsSelf(svc.Backend, selfPort, "") {
			selfs = append(selfs, svc.Name)
		}
	}
	if len(selfs) == 0 {
		return
	}
	diag.Logf(diag.ConfigError, "", "检测到 %d 个自指服务（target 指向网关自身 :%d）：%s；"+
		"链路会再进一次网关，链式跳数上限 %d，超过即 508",
		len(selfs), selfPort, strings.Join(selfs, ", "), maxHops)
}

// Reload 用新表替换运行时配置，并写审计（FR-4：变更可审计）。
func (e *Engine) Reload(tbl *config.Table, actor string) {
	if tbl == nil {
		return
	}
	e.apply(tbl)
	e.audit.Append(diag.Entry{
		Actor:  actor,
		Action: "reload",
		Detail: fmt.Sprintf("hash=%s services=%d", tbl.Hash, len(tbl.Services)),
	})
	diag.Logf(diag.Lifecycle, "", "配置已重新加载：%d 个服务（hash=%s，来源 %s）", len(tbl.Services), tbl.Hash, tbl.Source)
	for _, w := range tbl.Warnings {
		diag.Logf(diag.Lifecycle, "", "配置提示：%s", w)
	}
}

// ReloadFromFile 从文件重新加载；失败时保留原配置（不因一次手抖把网关打挂）。
func (e *Engine) ReloadFromFile(path, actor string) (*config.Table, error) {
	tbl, err := config.Load(path)
	if err != nil {
		diag.Logf(diag.ConfigError, "", "配置重新加载失败，继续使用原配置: %v", err)
		e.audit.Append(diag.Entry{Actor: actor, Action: "reload_failed", Detail: err.Error()})
		return nil, err
	}
	e.Reload(tbl, actor)
	return tbl, nil
}

// SetMode 热切换单个服务的处理模式（FR-14 一键降级为 isolated）。
func (e *Engine) SetMode(name string, mode config.Mode, actor string) error {
	if !mode.Valid() {
		return fmt.Errorf("未知模式: %s", mode)
	}
	rt, ok := e.runtime(name)
	if !ok {
		return fmt.Errorf("服务不存在: %s", name)
	}
	from := rt.setMode(mode)
	e.audit.Append(diag.Entry{
		Actor: actor, Action: "set_mode", Service: rt.name,
		From: string(from), To: string(mode),
	})
	diag.Logf(diag.Lifecycle, rt.name, "处理模式切换：%s → %s（操作者 %s）", from, mode, actor)
	return nil
}

// runtime 按名字取运行时（大小写不敏感）。
func (e *Engine) runtime(name string) (*runtime, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rt, ok := e.runtimes[strings.ToLower(name)]
	return rt, ok
}

// Matches 判断路径是否落在隔离服务的首段内（用于路由决策）。
// 服务被禁用时仍返回 true：由引擎返回 503，而不是把请求漏给 SPA 前端。
func (e *Engine) Matches(path string) bool {
	e.mu.RLock()
	m := e.matcher
	e.mu.RUnlock()
	_, _, ok := m.Lookup(path)
	return ok
}

// HasPrefix 判断首段是否为隔离服务名（供部署前缀剥离逻辑避让）。
func (e *Engine) HasPrefix(first string) bool {
	e.mu.RLock()
	m := e.matcher
	e.mu.RUnlock()
	return m.Has(first)
}

// AttributeUnmatched 对「不归任何服务」的路径做越界归因（只写指标与诊断日志，
// 不改变响应，绝不据此路由）。供路由层在 404 / SPA 回退之前调用。
func (e *Engine) AttributeUnmatched(path string) {
	if e == nil {
		return
	}
	e.mu.RLock()
	m := e.matcher
	e.mu.RUnlock()
	if owner, pfx, hit := m.Owner(path); hit {
		e.noteAbsPrefix(owner, pfx, path)
	}
}

// Active 表示隔离网关是否真的接管了路径空间（至少声明了一个服务）。
// 未声明任何服务时保持 false：既有的部署前缀剥离/DB 路由行为完全不变。
func (e *Engine) Active() bool { return e != nil && e.Count() > 0 }

// Count 返回已配置的服务数量。
func (e *Engine) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.runtimes)
}

// Table 返回当前配置快照（只读，调用方不得修改）。
func (e *Engine) Table() *config.Table {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.table
}

// SetFileLogger 注入访问日志落盘器（运行期可切换；nil 表示不落盘）。
func (e *Engine) SetFileLogger(l *accesslog.FileLogger) {
	e.mu.Lock()
	e.logger = l
	e.mu.Unlock()
}

// ServeHTTP 执行单个请求的完整处理循环，并落一条访问记录。
//
// 访问日志与普通反代共用同一个写入器（log/requests/<date>/<HH>.log），
// 字段也一致，只把 route_prefix 记成**服务前缀**、kind 记成 isolation。
// 隔离链路此前完全不写访问日志，导致 /openlist/ 这类请求在日志里 0 条，
// 排查「页面打不开」时被误导了两次——所以这里连「未匹配 404 / 服务禁用 /
// 熔断降级」这些早退分支也要记，不能只记成功转发的。
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := newRecorder(w)
	start := time.Now()

	// 请求体片段必须在转发前嗅探（SniffBody 会把读流完整还原）
	var body string
	if e.loggerOf().BodyEnabled() {
		body = accesslog.SniffBody(r)
	}

	// 改写审计载体：与 DB 路由链路共用同一套字段与日志类别。
	rewriteAudit := &rewrite.Audit{}
	r = rewrite.WithAudit(r, rewriteAudit)

	prefix := e.serve(rec, r)
	elapsed := time.Since(start)

	e.logRewrite(r, prefix, rewriteAudit)

	if lg := e.loggerOf(); lg != nil {
		err := lg.Log(accesslog.Entry{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			IP:      forward.ClientIP(r.RemoteAddr, r.Header, e.trustProxies),
			UA:      r.UserAgent(),
			Status:  rec.Status(),
			Size:    rec.Bytes(),
			Latency: elapsed.String(),
			Headers: accesslog.HeadersOf(r.Header),
			Body:    body,
			// 与 DB 路由的前缀写法对齐（去掉前导 /，如 openlist 而非 /openlist）
			RoutePrefix: strings.TrimPrefix(prefix, "/"),
			Kind:        "isolation",
		})
		if err != nil {
			diag.Logf(diag.ConfigError, prefix, "访问日志写入失败: %v", err)
		}
	}
}

// logRewrite 把隔离链路的改写行为落到 log/rewrite/，与普通代理同一类别、同一字段。
func (e *Engine) logRewrite(r *http.Request, prefix string, a *rewrite.Audit) {
	lg := e.loggerOf()
	if lg == nil || a == nil || (a.Hits == 0 && a.Skip == "") {
		return
	}
	err := lg.LogCategory(accesslog.CategoryRewrite, accesslog.Entry{
		Method:        r.Method,
		Path:          r.URL.Path,
		Query:         r.URL.RawQuery,
		IP:            forward.ClientIP(r.RemoteAddr, r.Header, e.trustProxies),
		UA:            r.UserAgent(),
		RoutePrefix:   strings.TrimPrefix(prefix, "/"),
		Kind:          "isolation",
		RewriteHits:   a.Hits,
		RewriteChain:  a.Chain,
		RewriteSkip:   a.Skip,
		RewriteKind:   a.Kind,
		RewriteStatus: a.Status,
	})
	if err != nil {
		diag.Logf(diag.ConfigError, prefix, "改写审计落盘失败: %v", err)
	}
}

// loggerOf 读当前日志器（热切换安全）。
func (e *Engine) loggerOf() *accesslog.FileLogger {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.logger
}

// serve 执行一次请求处理，返回命中的服务前缀（未命中返回空串）。
func (e *Engine) serve(w *recorder, r *http.Request) string {
	e.mu.RLock()
	m := e.matcher
	e.mu.RUnlock()

	svc, rest, ok := m.Lookup(r.URL.Path)
	if !ok {
		// 越界归因（FR-2 的 known_abs_prefixes「仅用于日志与告警」）：
		// 该路径虽然不归任何服务，但可能正是某服务后端前端「没配 base path」后打出来的
		// 根路径请求（典型：/assets/app.js、/api/public/settings）。这里只归因 + 计数，
		// 绝不据此转发（FR-3）。
		if owner, pfx, hit := m.Owner(r.URL.Path); hit {
			e.noteAbsPrefix(owner, pfx, r.URL.Path)
		}
		// FR-3：未匹配不猜测归属，直接 404
		writeJSON(w, http.StatusNotFound, "route not found")
		return ""
	}
	rt, ok := e.runtime(svc.Name)
	if !ok {
		writeJSON(w, http.StatusNotFound, "route not found")
		return svc.Prefix
	}
	cfg := rt.service()
	cnt := rt.counters

	cnt.Requests.Add(1)
	cnt.LastSeenUnix.Store(time.Now().Unix())

	if !cfg.Enabled {
		diag.Logf(diag.ProxyDegrade, cfg.Name, "服务已禁用，请求被拒绝: %s %s", r.Method, r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, "service disabled")
		return cfg.Prefix
	}

	// 自指/嵌套：跳数超上限直接 508，不再把请求推给自己。
	// 放在熔断之前 —— 绕圈不是后端故障，不该把服务熔断掉。
	hops, ok := e.hops.Next(r.Header)
	if !ok {
		cnt.LoopAlerts.Add(1)
		diag.Logf(diag.LoopWarning, cfg.Name, "转发跳数超过上限 %d，判定为自指死循环: %s %s",
			e.hops.Max(), r.Method, r.URL.Path)
		writeJSON(w, http.StatusLoopDetected, "loop detected: 转发跳数超过上限")
		return cfg.Prefix
	}
	hopguard.Stamp(r.Header, hops)

	// 熔断打开：直接降级，不再排队占用后端连接（NFR-4）
	if !rt.breaker.Allow() {
		cnt.BreakerTrips.Add(1)
		diag.Logf(diag.ProxyDegrade, cfg.Name, "熔断中，请求直接降级: %s %s", r.Method, r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, "service temporarily unavailable (circuit open)")
		return cfg.Prefix
	}

	st := &requestState{
		cfg:      cfg,
		rest:     rest,
		prefix:   cfg.Prefix,
		clientIP: forward.ClientIP(r.RemoteAddr, r.Header, e.trustProxies),
		origin:   r.Host,
		proto:    forward.Proto(r),
		counters: cnt,
	}
	ctx := context.WithValue(r.Context(), stateKey{}, st)
	req := r.WithContext(ctx)
	if !forward.IsUpgrade(req) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.TimeoutDuration())
		defer cancel()
		req = req.WithContext(ctx)
	}

	start := time.Now()
	rt.proxy.ServeHTTP(w, req)
	elapsed := time.Since(start)

	cnt.TotalLatencyMs.Add(elapsed.Milliseconds())
	cnt.AddStatus(w.Status())

	// 重定向循环告警（同一来源短时间反复 3xx）
	if w.Status() >= 300 && w.Status() < 400 && st.clientIP != "" {
		if e.loop.Hit(cfg.Name + "|" + st.clientIP) {
			cnt.LoopAlerts.Add(1)
			diag.Logf(diag.LoopWarning, cfg.Name, "疑似重定向循环：%s 在时间窗内重定向次数超阈值（%s）",
				st.clientIP, r.URL.Path)
		}
	}
	return cfg.Prefix
}

// ServiceStatus 是「配置 + 运行状态」的合并视图（FR-19）。
type ServiceStatus struct {
	Name             string           `json:"name"`
	Prefix           string           `json:"prefix"`
	Target           string           `json:"target"`
	Mode             string           `json:"mode"`
	Enabled          bool             `json:"enabled"`
	CookieHat        string           `json:"cookie_hat"`
	HTMLRewrite      bool             `json:"html_rewrite"`
	KnownAbsPrefixes []string         `json:"known_abs_prefixes,omitempty"`
	Timeout          int              `json:"timeout"`
	HostMode         string           `json:"host_mode"`
	NoFullCompat     bool             `json:"no_full_compat"`
	Breaker          breaker.Snapshot `json:"breaker"`
	Metrics          metrics.Snapshot `json:"metrics"`
}

// Snapshot 返回按服务名排序的配置 + 运行状态。
func (e *Engine) Snapshot() []ServiceStatus {
	e.mu.RLock()
	runtimes := make(map[string]*runtime, len(e.runtimes))
	for k, v := range e.runtimes {
		runtimes[k] = v
	}
	e.mu.RUnlock()

	out := make([]ServiceStatus, 0, len(runtimes))
	for _, rt := range runtimes {
		cfg := rt.service()
		out = append(out, ServiceStatus{
			Name:             cfg.Name,
			Prefix:           cfg.Prefix,
			Target:           cfg.Target,
			Mode:             cfg.Mode.Canonical(),
			Enabled:          cfg.Enabled,
			CookieHat:        cfg.CookieHat,
			HTMLRewrite:      cfg.HTMLRewrite,
			KnownAbsPrefixes: cfg.KnownAbsPrefix,
			Timeout:          cfg.Timeout,
			HostMode:         cfg.HostMode.Canonical(),
			NoFullCompat:     cfg.NoFullCompat,
			Breaker:          rt.breaker.Snapshot(),
			Metrics:          rt.counters.Snapshot(cfg.Name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// AuditRecent 返回最近的配置变更审计记录（供管理接口查询）。
func (e *Engine) AuditRecent(n int) []diag.Entry {
	if e == nil || e.audit == nil {
		return nil
	}
	return e.audit.Recent(n)
}

// AuditPath 返回审计文件路径。
func (e *Engine) AuditPath() string {
	if e == nil || e.audit == nil {
		return ""
	}
	return e.audit.Path()
}

// Metrics 返回纯指标快照。
func (e *Engine) Metrics() []metrics.Snapshot { return e.metrics.Snapshot() }

// Warnings 返回当前配置的提示项。
func (e *Engine) Warnings() []string {
	tbl := e.Table()
	if tbl == nil {
		return nil
	}
	return tbl.Warnings
}

// writeJSON 输出统一错误结构（与网关其余接口一致）。
func writeJSON(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"code":%d,"message":%q}`, code, msg)
}

// selfLoopWindow 是「重定向自环」判定的观察窗口：同一 服务+来源+后端路径 在窗口内
// 第 2 次命中才拦截，给「302 到同一路径 + 种 Cookie」这类一次性自跳留出余地。
const selfLoopWindow = 60 * time.Second

// noteAbsPrefix 记录一次「后端前端越过服务前缀」的越界请求：按服务计数，并按
// 服务+前缀节流打日志（60 秒一条），把责任边界写清楚（FR-18）。
//
// 典型现场：openlist 没配 site_url 时，前端运行时把资源与 API 拼成根路径
// （/assets/x.js、/api/public/settings），浏览器于是直接打到网关根 —— 这类请求
// 现在会被明确归因到服务，而不是只留一条没人看得懂的 404。
func (e *Engine) noteAbsPrefix(owner *config.Service, prefix, path string) {
	if c := e.metrics.For(owner.Name); c != nil {
		c.AbsPrefixHits.Add(1)
	}
	key := strings.ToLower(owner.Name) + "|" + prefix
	now := time.Now().Unix()
	if v, ok := e.absHints.Load(key); ok {
		if last, _ := v.(int64); now-last < 60 {
			return
		}
	}
	e.absHints.Store(key, now)
	diag.Logf(diag.ConfigError, owner.Name,
		"未匹配路径 %s 命中该服务声明的已知绝对前缀 %q：这是后端前端没配 base path 造成的越界请求"+
			"（浏览器直接请求了网关根路径），已按 FR-3 返回 404、不猜测归属；"+
			"处理方式：让后端配置自身 base path（如 openlist 的 site_url 带 /%s）并把该服务改为 pass-through",
		path, prefix, owner.Name)
}
