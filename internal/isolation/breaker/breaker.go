package breaker

import (
	"sync"
	"time"
)

// State 是熔断器状态。
type State string

const (
	StateClosed   State = "closed"    // 正常放行
	StateOpen     State = "open"      // 已熔断，直接降级
	StateHalfOpen State = "half_open" // 冷却结束，放行一次探测
)

// Breaker 是「连续失败即熔断」的极简熔断器（NFR-4）。
//
// 设计取舍：只做「连续失败 + 冷却 + 单次半开探测」，不引入滑动窗口与错误率统计——
// 反向代理需要的是「后端挂了就别再排队占资源」，而不是精细的 SRE 指标。
// nil 指针安全：threshold<=0 时 New 返回 nil，所有方法在 nil 上都是「永远放行」。
type Breaker struct {
	threshold int
	cooldown  time.Duration
	now       func() time.Time

	mu       sync.Mutex
	state    State
	consec   int
	openedAt time.Time
	trips    int64
}

// New 创建熔断器；threshold<=0 返回 nil（表示禁用熔断）。
func New(threshold int, cooldown time.Duration) *Breaker {
	if threshold <= 0 {
		return nil
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &Breaker{threshold: threshold, cooldown: cooldown, now: time.Now, state: StateClosed}
}

// Allow 判断本次请求是否可以发往后端。
func (b *Breaker) Allow() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateOpen:
		if b.now().Sub(b.openedAt) >= b.cooldown {
			b.state = StateHalfOpen
			return true // 只放行这一发探测，其余继续降级
		}
		return false
	case StateHalfOpen:
		return false
	}
	return true
}

// Success 记录一次成功（关闭熔断、清零连续失败）。
func (b *Breaker) Success() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateClosed
	b.consec = 0
	b.openedAt = time.Time{}
}

// Failure 记录一次失败，返回本次是否「刚刚熔断」（用于只打一条日志）。
func (b *Breaker) Failure() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consec++
	if b.state == StateHalfOpen || b.consec >= b.threshold {
		b.state = StateOpen
		b.openedAt = b.now()
		b.trips++
		return true
	}
	return false
}

// Snapshot 是熔断器对外状态。
type Snapshot struct {
	State         State `json:"state"`
	Consecutive   int   `json:"consecutive_failures"`
	Trips         int64 `json:"trips"`
	OpenUntilUnix int64 `json:"open_until_unix"`
}

// Snapshot 返回当前状态（nil 视为 closed）。
func (b *Breaker) Snapshot() Snapshot {
	if b == nil {
		return Snapshot{State: StateClosed}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Snapshot{State: b.state, Consecutive: b.consec, Trips: b.trips}
	if b.state != StateClosed && !b.openedAt.IsZero() {
		s.OpenUntilUnix = b.openedAt.Add(b.cooldown).Unix()
	}
	return s
}
