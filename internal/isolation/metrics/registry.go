package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counters 是单服务的运行指标（全原子自增，无锁热路径，满足 NFR-3 可并发）。
type Counters struct {
	Requests         atomic.Int64
	Status2xx        atomic.Int64
	Status3xx        atomic.Int64
	Status4xx        atomic.Int64
	Status5xx        atomic.Int64
	LocationRewrites atomic.Int64
	CookieRewrites   atomic.Int64
	CookiesDropped   atomic.Int64
	HTMLHits         atomic.Int64
	HTMLFailures     atomic.Int64
	BodySkipped      atomic.Int64
	BackendErrors    atomic.Int64
	BreakerTrips     atomic.Int64
	LoopAlerts       atomic.Int64
	// AbsPrefixHits 是「后端前端越过服务前缀、请求打到网关根路径」的次数
	// （命中 known_abs_prefixes 但未带服务名，已 404）。>0 说明该后端没配 base path。
	AbsPrefixHits  atomic.Int64
	TotalLatencyMs atomic.Int64
	LastSeenUnix   atomic.Int64
}

// AddStatus 按状态码区间累计。
func (c *Counters) AddStatus(code int) {
	switch {
	case code >= 500:
		c.Status5xx.Add(1)
	case code >= 400:
		c.Status4xx.Add(1)
	case code >= 300:
		c.Status3xx.Add(1)
	case code >= 200:
		c.Status2xx.Add(1)
	}
}

// Snapshot 是单服务的对外指标快照（JSON 字段稳定，供 API 直出）。
type Snapshot struct {
	Service          string  `json:"service"`
	Requests         int64   `json:"requests"`
	Status2xx        int64   `json:"status_2xx"`
	Status3xx        int64   `json:"status_3xx"`
	Status4xx        int64   `json:"status_4xx"`
	Status5xx        int64   `json:"status_5xx"`
	Rate4xx          float64 `json:"rate_4xx"`
	Rate5xx          float64 `json:"rate_5xx"`
	LocationRewrites int64   `json:"location_rewrites"`
	CookieRewrites   int64   `json:"cookie_rewrites"`
	CookiesDropped   int64   `json:"cookies_dropped"`
	HTMLHits         int64   `json:"html_hits"`
	HTMLFailures     int64   `json:"html_failures"`
	BodySkipped      int64   `json:"body_skipped"`
	BackendErrors    int64   `json:"backend_errors"`
	BreakerTrips     int64   `json:"breaker_trips"`
	LoopAlerts       int64   `json:"loop_alerts"`
	AbsPrefixHits    int64   `json:"abs_prefix_hits"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	LastSeenUnix     int64   `json:"last_seen_unix"`
}

// Snapshot 生成快照（名额缺失时返回零值快照）。
func (c *Counters) Snapshot(service string) Snapshot {
	s := Snapshot{
		Service:          service,
		Requests:         c.Requests.Load(),
		Status2xx:        c.Status2xx.Load(),
		Status3xx:        c.Status3xx.Load(),
		Status4xx:        c.Status4xx.Load(),
		Status5xx:        c.Status5xx.Load(),
		LocationRewrites: c.LocationRewrites.Load(),
		CookieRewrites:   c.CookieRewrites.Load(),
		CookiesDropped:   c.CookiesDropped.Load(),
		HTMLHits:         c.HTMLHits.Load(),
		HTMLFailures:     c.HTMLFailures.Load(),
		BodySkipped:      c.BodySkipped.Load(),
		BackendErrors:    c.BackendErrors.Load(),
		BreakerTrips:     c.BreakerTrips.Load(),
		LoopAlerts:       c.LoopAlerts.Load(),
		AbsPrefixHits:    c.AbsPrefixHits.Load(),
		LastSeenUnix:     c.LastSeenUnix.Load(),
	}
	if s.Requests > 0 {
		s.Rate4xx = round4(float64(s.Status4xx) / float64(s.Requests))
		s.Rate5xx = round4(float64(s.Status5xx) / float64(s.Requests))
		s.AvgLatencyMs = round4(float64(c.TotalLatencyMs.Load()) / float64(s.Requests))
	}
	return s
}

func round4(f float64) float64 {
	return float64(int64(f*10000+0.5)) / 10000
}

// Registry 是按服务名分组的指标注册表。
type Registry struct {
	mu sync.RWMutex
	m  map[string]*Counters
}

// NewRegistry 创建注册表。
func NewRegistry() *Registry {
	return &Registry{m: make(map[string]*Counters)}
}

// For 取（或创建）某服务的计数器。热路径调用，读多写极少。
func (r *Registry) For(service string) *Counters {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	c, ok := r.m[service]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok = r.m[service]; ok {
		return c
	}
	c = &Counters{}
	r.m[service] = c
	return c
}

// Snapshot 返回按服务名排序的全量指标。
func (r *Registry) Snapshot() []Snapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.m))
	for k := range r.m {
		names = append(names, k)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	out := make([]Snapshot, 0, len(names))
	for _, n := range names {
		out = append(out, r.For(n).Snapshot(n))
	}
	return out
}

// Touch 记录最后一次访问时间（供运行状态展示）。
func (r *Registry) Touch(service string) {
	if c := r.For(service); c != nil {
		c.LastSeenUnix.Store(time.Now().Unix())
	}
}
