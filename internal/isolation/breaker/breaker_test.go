package breaker

import (
	"testing"
	"time"
)

func TestNilBreakerAlwaysAllows(t *testing.T) {
	var b *Breaker
	if !b.Allow() {
		t.Fatal("nil 熔断器应永远放行")
	}
	if b.Failure() {
		t.Fatal("nil 熔断器不应报告熔断")
	}
	b.Success()
	if s := b.Snapshot(); s.State != StateClosed {
		t.Fatalf("nil 快照状态 = %v", s.State)
	}
	if New(0, time.Second) != nil {
		t.Fatal("threshold<=0 应返回 nil")
	}
}

func TestTripsAfterConsecutiveFailures(t *testing.T) {
	base := time.Now()
	b := New(3, 10*time.Second)
	b.now = func() time.Time { return base }

	for i := 0; i < 2; i++ {
		if !b.Allow() {
			t.Fatalf("第 %d 次应放行", i+1)
		}
		if b.Failure() {
			t.Fatalf("第 %d 次失败不应熔断", i+1)
		}
	}
	if b.Success(); b.Snapshot().Consecutive != 0 {
		t.Fatal("成功后连续失败数应清零")
	}
	// 连续 3 次失败（阈值 3）触发熔断
	b.Failure()
	b.Failure()
	if b.Failure() != true {
		t.Fatal("第 3 次连续失败应触发熔断")
	}
	if b.Allow() {
		t.Fatal("熔断期间应拒绝")
	}
	if s := b.Snapshot(); s.State != StateOpen || s.Trips != 1 || s.OpenUntilUnix == 0 {
		t.Fatalf("快照异常: %+v", s)
	}

	// 冷却结束：半开只放行一次
	base = base.Add(11 * time.Second)
	if !b.Allow() {
		t.Fatal("冷却结束应放行一次探测")
	}
	if b.Allow() {
		t.Fatal("半开状态只允许一次探测")
	}
	b.Success()
	if s := b.Snapshot(); s.State != StateClosed {
		t.Fatalf("探测成功后应关闭熔断: %+v", s)
	}
	if !b.Allow() {
		t.Fatal("关闭后应正常放行")
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	base := time.Now()
	b := New(1, time.Second)
	b.now = func() time.Time { return base }
	b.Failure() // 立即熔断
	base = base.Add(2 * time.Second)
	if !b.Allow() {
		t.Fatal("冷却后应放行探测")
	}
	if !b.Failure() {
		t.Fatal("探测失败应重新熔断")
	}
	if b.Allow() {
		t.Fatal("重新熔断后应拒绝")
	}
	if b.Snapshot().Trips != 2 {
		t.Fatalf("熔断次数应为 2: %+v", b.Snapshot())
	}
}
