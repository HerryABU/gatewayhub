package metrics

import (
	"sync"
	"time"
)

// LoopDetector 检测「同一来源在短时间内的重定向次数是否超阈值」（FR-17 重定向循环告警）。
//
// 判定键由调用方给出（建议 服务名 + 客户端 IP），窗口内累计 3xx 次数，
// 越过阈值只告警一次（避免每跳一条日志把磁盘刷满），窗口滑动后重新计数。
type LoopDetector struct {
	threshold int
	window    time.Duration
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	start time.Time
	count int
	alert bool
}

// NewLoopDetector 创建检测器；threshold<=0 或 window<=0 时视为禁用（Hit 恒为 false）。
func NewLoopDetector(threshold int, window time.Duration) *LoopDetector {
	return &LoopDetector{
		threshold: threshold,
		window:    window,
		now:       time.Now,
		buckets:   make(map[string]*bucket),
	}
}

// Hit 记一次 3xx，返回本次是否「刚刚越过阈值」（同一窗口内只返回一次 true）。
func (d *LoopDetector) Hit(key string) bool {
	if d == nil || d.threshold <= 0 || d.window <= 0 || key == "" {
		return false
	}
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()

	b, ok := d.buckets[key]
	if !ok || now.Sub(b.start) > d.window {
		if !ok && len(d.buckets) > 4096 {
			d.purgeLocked(now) // 极端长尾场景下防止 map 无限增长
		}
		b = &bucket{start: now}
		d.buckets[key] = b
	}
	b.count++
	if !b.alert && b.count > d.threshold {
		b.alert = true
		return true
	}
	return false
}

// purgeLocked 清理过期窗口（调用方需持锁）。
func (d *LoopDetector) purgeLocked(now time.Time) {
	for k, b := range d.buckets {
		if now.Sub(b.start) > d.window {
			delete(d.buckets, k)
		}
	}
}

// Len 返回当前跟踪的来源数量（测试与状态展示用）。
func (d *LoopDetector) Len() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.buckets)
}
