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

	"gatewayhub/internal/isolation/breaker"
	"gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/diag"
	"gatewayhub/internal/isolation/forward"
	"gatewayhub/internal/isolation/matcher"
	"gatewayhub/internal/isolation/metrics"
)

// Options 是引擎依赖注入项（全部可空，缺省时内部自建）。
type Options struct {
	Metrics *metrics.Registry
	Audit   *diag.Audit
	Loop    *metrics.LoopDetector
	Table   *config.Table
	// TrustProxies 表示前面是否还有可信反代（决定客户端 IP 是否取 XFF/X-Real-IP）。
	TrustProxies bool
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

	loopInjected bool
	// trustProxies 决定客户端 IP 是否取 XFF/X-Real-IP（前面有可信反代时才可开）
	trustProxies bool
}

// New 创建引擎；table 为 nil 时创建一个空表（此时引擎不匹配任何路径，等价于未启用）。
func New(opts Options) *Engine {
	e := &Engine{
		runtimes:     make(map[string]*runtime),
		metrics:      opts.Metrics,
		audit:        opts.Audit,
		pool:         newTransportPool(),
		trustProxies: opts.TrustProxies,
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
		runtimes[strings.ToLower(svc.Name)] = newRuntime(svc, e.pool, e.metrics, tbl.Breaker)
	}
	m := matcher.New(tbl.Services)

	e.mu.Lock()
	e.matcher = m
	e.runtimes = runtimes
	e.table = tbl
	if !e.loopInjected {
		e.loop = metrics.NewLoopDetector(tbl.Loop.Threshold, time.Duration(tbl.Loop.Window)*time.Second)
	}
	e.mu.Unlock()
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

// ServeHTTP 执行单个请求的完整处理循环。
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.RLock()
	m := e.matcher
	e.mu.RUnlock()

	svc, rest, ok := m.Lookup(r.URL.Path)
	if !ok {
		// FR-3：未匹配不猜测归属，直接 404
		writeJSON(w, http.StatusNotFound, "route not found")
		return
	}
	rt, ok := e.runtime(svc.Name)
	if !ok {
		writeJSON(w, http.StatusNotFound, "route not found")
		return
	}
	cfg := rt.service()
	cnt := rt.counters

	cnt.Requests.Add(1)
	cnt.LastSeenUnix.Store(time.Now().Unix())

	if !cfg.Enabled {
		diag.Logf(diag.ProxyDegrade, cfg.Name, "服务已禁用，请求被拒绝: %s %s", r.Method, r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, "service disabled")
		return
	}

	// 熔断打开：直接降级，不再排队占用后端连接（NFR-4）
	if !rt.breaker.Allow() {
		cnt.BreakerTrips.Add(1)
		diag.Logf(diag.ProxyDegrade, cfg.Name, "熔断中，请求直接降级: %s %s", r.Method, r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, "service temporarily unavailable (circuit open)")
		return
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

	rec := newRecorder(w)
	start := time.Now()
	rt.proxy.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	cnt.TotalLatencyMs.Add(elapsed.Milliseconds())
	cnt.AddStatus(rec.Status())

	// 重定向循环告警（同一来源短时间反复 3xx）
	if rec.Status() >= 300 && rec.Status() < 400 && st.clientIP != "" {
		if e.loop.Hit(cfg.Name + "|" + st.clientIP) {
			cnt.LoopAlerts.Add(1)
			diag.Logf(diag.LoopWarning, cfg.Name, "疑似重定向循环：%s 在时间窗内重定向次数超阈值（%s）",
				st.clientIP, r.URL.Path)
		}
	}
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
