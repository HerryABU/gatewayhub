package health

import (
	"context"
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
}

// Info 健康信息（供 API 返回）
type Info struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
}

// HistoryPoint 一次探测结果（用于分段状态条）
type HistoryPoint struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Time      int64  `json:"time"` // unix 毫秒
}

// historyCap 每路由保留的最近探测点数量（分段状态条宽度）
const historyCap = 90

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
		resp, err := c.client.Do(req)
		cancel()
		latency := time.Since(start).Milliseconds()
		if err == nil {
			resp.Body.Close()
			return true, latency
		}
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

// InfoMap 返回前缀到健康信息的映射
func (c *Checker) InfoMap() map[string]Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]Info, len(c.state))
	for k, v := range c.state {
		m[k] = Info{Status: v.status, LatencyMs: v.latencyMs}
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
