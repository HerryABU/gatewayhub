package health

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"gatewayhub/internal/config"
	"gatewayhub/internal/models"
	"gatewayhub/internal/target"
)

// Checker 健康检查器（含延迟测量与绿/橙/红状态）
type Checker struct {
	db               *gorm.DB
	interval         time.Duration
	timeout          time.Duration
	slowThreshold    time.Duration
	failThreshold    int
	recoverThreshold int
	healthEndpoint   string
	client           *http.Client
	mu               sync.RWMutex
	state            map[string]*routeHealth
	stop             chan struct{}
}

type routeHealth struct {
	status          string
	latencyMs       int64
	consecutiveFail int
	consecutiveOK   int
	lastChecked     time.Time
	history         []HistoryPoint
	// 证书有效期（来自后端 TLS 证书），供访客页展示；每 certProbeInterval 更新一次。
	certDays      int
	certCheckedAt time.Time
}

// certProbeInterval 证书探测间隔：证书有效期变化以天计，无需跟随每次健康检查。
const certProbeInterval = 5 * time.Minute

// Info 健康信息（供 API 返回）
type Info struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	// CertDays 后端 TLS 证书剩余天数；0 表示不适用（HTTP / 探测失败 / 无域名）。
	CertDays int `json:"cert_days"`
}

// HistoryPoint 一次探测结果（用于分段状态条）
type HistoryPoint struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Time      int64  `json:"time"` // unix 毫秒
}

// historyCap 每路由保留的最近探测点数量（分段状态条宽度）
const historyCap = 90

// insecureClient 返回仅用于「TLS 跳过校验」路由探测的 client。
// 按超时缓存，避免每条路由每次探测都新建 Transport（连接池会失效）。
var insecureClients sync.Map // timeout -> *http.Client

func insecureClient(timeout time.Duration) *http.Client {
	if c, ok := insecureClients.Load(timeout); ok {
		return c.(*http.Client)
	}
	c := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
	actual, _ := insecureClients.LoadOrStore(timeout, c)
	return actual.(*http.Client)
}

// New 创建健康检查器
func New(db *gorm.DB, cfg config.HealthConfig) *Checker {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	slow := time.Duration(cfg.SlowThreshold) * time.Millisecond
	if slow <= 0 {
		slow = 1000 * time.Millisecond
	}
	return &Checker{
		db:               db,
		interval:         time.Duration(cfg.Interval) * time.Second,
		timeout:          timeout,
		slowThreshold:    slow,
		failThreshold:    cfg.FailThreshold,
		recoverThreshold: cfg.RecoverThreshold,
		healthEndpoint:   cfg.HealthEndpoint,
		client:           &http.Client{Timeout: timeout},
		state:            make(map[string]*routeHealth),
		stop:             make(chan struct{}),
	}
}

// Start 启动后台探测循环
func (c *Checker) Start() {
	if c.failThreshold <= 0 {
		c.failThreshold = 3
	}
	if c.recoverThreshold <= 0 {
		c.recoverThreshold = 2
	}
	if c.interval <= 0 {
		c.interval = 30 * time.Second
	}
	// 立即执行首轮探测，避免启动初期无数据
	go c.CheckAll()
	go c.loop()
}

func (c *Checker) loop() {
	// 以较细粒度轮询，逐条判断是否到期（每条路由可按自身间隔探测）
	tick := 5 * time.Second
	if c.interval > 0 && c.interval < tick {
		tick = c.interval
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.CheckDue()
		case <-c.stop:
			return
		}
	}
}

// CheckAll 立即检查所有活跃路由（公开，供手动触发）
func (c *Checker) CheckAll() {
	var routes []models.Route
	if err := c.db.Find(&routes).Error; err != nil {
		return
	}
	for _, r := range routes {
		if r.Status != models.StatusActive {
			continue // 停用站点不探测
		}
		up, latency := c.checkRoute(r)
		c.update(r.Prefix, up, latency)
		c.refreshCert(r.Prefix, r.Target)
	}
}

// CheckDue 检查到期的活跃路由（每条路由按其自身间隔）
func (c *Checker) CheckDue() {
	var routes []models.Route
	if err := c.db.Find(&routes).Error; err != nil {
		return
	}
	now := time.Now()
	for _, r := range routes {
		if r.Status != models.StatusActive {
			continue // 停用站点不探测
		}
		c.mu.RLock()
		h, ok := c.state[r.Prefix]
		c.mu.RUnlock()
		if ok && now.Sub(h.lastChecked) < c.routeInterval(r) {
			continue
		}
		up, latency := c.checkRoute(r)
		c.update(r.Prefix, up, latency)
		c.refreshCert(r.Prefix, r.Target)
	}
}

// routeInterval 返回该路由的探测间隔（秒）；未设置则用全局默认。
func (c *Checker) routeInterval(r models.Route) time.Duration {
	secs := r.Interval
	if secs <= 0 {
		secs = int(c.interval / time.Second)
	}
	if secs <= 0 {
		secs = 30
	}
	return time.Duration(secs) * time.Second
}

// checkRoute 探测单个路由，返回 (是否可用, 延迟毫秒)
func (c *Checker) checkRoute(r models.Route) (bool, int64) {
	p, err := target.Parse(r.Target)
	if err != nil {
		return false, 0
	}
	healthURL := p.Scheme + "://" + p.Host + c.healthEndpoint

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err == nil {
		if resp, err := c.client.Do(req); err == nil {
			cancel()
			resp.Body.Close()
			return true, time.Since(start).Milliseconds()
		}
	}
	cancel()
	// 自签后端（宝塔面板）须按路由开关重试，否则明明能反代却显示 down。
	// 复用独立 client，避免改动全局默认校验策略。
	if r.TLSSkipVerify && p.Scheme == "https" {
		start = time.Now()
		ctx2, cancel2 := context.WithTimeout(context.Background(), c.timeout)
		req2, err2 := http.NewRequestWithContext(ctx2, http.MethodGet, healthURL, nil)
		if err2 == nil {
			if resp, err2 := insecureClient(c.timeout).Do(req2); err2 == nil {
				cancel2()
				resp.Body.Close()
				return true, time.Since(start).Milliseconds()
			}
		}
		cancel2()
	}
	// TCP 兜底探测
	start = time.Now()
	conn, err := net.DialTimeout("tcp", p.Host, c.timeout)
	if err == nil {
		conn.Close()
		return true, time.Since(start).Milliseconds()
	}
	return false, 0
}

func (c *Checker) update(prefix string, up bool, latency int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.state[prefix]
	if !ok {
		h = &routeHealth{status: models.HealthUnknown}
		c.state[prefix] = h
	}
	h.lastChecked = time.Now()
	h.latencyMs = latency

	if up {
		h.consecutiveOK++
		h.consecutiveFail = 0
		// 缓慢判定：延迟超过阈值 → 橙
		if latency >= c.slowThreshold.Milliseconds() && c.slowThreshold.Milliseconds() > 0 {
			h.status = models.HealthWarning
		} else if h.consecutiveOK >= c.recoverThreshold || h.status == models.HealthUnknown {
			h.status = models.HealthHealthy
		}
	} else {
		h.consecutiveFail++
		h.consecutiveOK = 0
		if h.status == models.HealthUnknown || h.consecutiveFail >= c.failThreshold {
			h.status = models.HealthDown
		}
	}

	// 追加历史点（用于前端分段状态条）
	h.history = append(h.history, HistoryPoint{
		Status:    h.status,
		LatencyMs: latency,
		Time:      h.lastChecked.UnixMilli(),
	})
	if len(h.history) > historyCap {
		h.history = h.history[len(h.history)-historyCap:]
	}
}

// probeCertDays 读取后端 TLS 证书的剩余天数（HTTP 后端返回 0）。
// 不校验证书链：自签证书（宝塔等）也有有效期，访客页同样要显示。
// 失败一律返回 0（视为不适用），不影响健康检查状态。
func (c *Checker) probeCertDays(target_ string) int {
	p, err := target.Parse(target_)
	if err != nil || p.Scheme != "https" || p.Host == "" {
		return 0
	}
	host, _, err := net.SplitHostPort(p.Host)
	if err != nil {
		host = p.Host // 无端口形式，如 chat.deepseek.com
	}
	// tls.Dial 必须带端口；target.Parse 保留的是原始 host（常无端口）。
	dialAddr := p.Host
	if _, _, splitErr := net.SplitHostPort(dialAddr); splitErr != nil {
		dialAddr = net.JoinHostPort(host, "443")
	}
	dialer := &net.Dialer{Timeout: c.timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", dialAddr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // 只读取有效期，不校验信任链
	})
	if err != nil {
		return 0
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return 0
	}
	days := int(time.Until(certs[0].NotAfter).Hours() / 24)
	if days < 0 {
		days = 0 // 已过期显示 0，前端按红色处理
	}
	return days
}

// refreshCert 按间隔刷新证书有效期，结果写入 state（探测在锁外进行）。
func (c *Checker) refreshCert(prefix, target_ string) {
	c.mu.RLock()
	h, ok := c.state[prefix]
	due := !ok || h.certCheckedAt.IsZero() || time.Since(h.certCheckedAt) >= certProbeInterval
	c.mu.RUnlock()
	if !due {
		return
	}
	days := c.probeCertDays(target_)
	c.mu.Lock()
	if h, ok := c.state[prefix]; ok {
		h.certDays = days
		h.certCheckedAt = time.Now()
	}
	c.mu.Unlock()
}

// InfoMap 返回前缀到健康信息的映射
func (c *Checker) InfoMap() map[string]Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]Info, len(c.state))
	for k, v := range c.state {
		m[k] = Info{Status: v.status, LatencyMs: v.latencyMs, CertDays: v.certDays}
	}
	return m
}

// StatusMap 返回前缀到健康状态的映射（兼容旧接口）
func (c *Checker) StatusMap() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]string, len(c.state))
	for k, v := range c.state {
		m[k] = v.status
	}
	return m
}

// HistoryMap 返回前缀到历史探测点的映射（前端分段状态条）
func (c *Checker) HistoryMap() map[string][]HistoryPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string][]HistoryPoint, len(c.state))
	for k, v := range c.state {
		if len(v.history) == 0 {
			continue
		}
		cp := make([]HistoryPoint, len(v.history))
		copy(cp, v.history)
		m[k] = cp
	}
	return m
}

// Stop 停止探测
func (c *Checker) Stop() {
	close(c.stop)
}
