package metrics

import (
	"testing"
	"time"
)

func TestRegistryCounters(t *testing.T) {
	r := NewRegistry()
	c := r.For("A")
	if c != r.For("A") {
		t.Fatal("同一服务应复用同一计数器")
	}
	c.Requests.Add(4)
	c.AddStatus(200)
	c.AddStatus(204)
	c.AddStatus(302)
	c.AddStatus(404)
	c.AddStatus(503)
	c.LocationRewrites.Add(2)
	c.HTMLHits.Add(7)
	c.TotalLatencyMs.Add(400)

	snap := r.For("A").Snapshot("A")
	if snap.Requests != 4 || snap.Status2xx != 2 || snap.Status3xx != 1 || snap.Status4xx != 1 || snap.Status5xx != 1 {
		t.Fatalf("状态码分桶异常: %+v", snap)
	}
	if snap.Rate4xx != 0.25 || snap.Rate5xx != 0.25 {
		t.Fatalf("比例计算异常: %+v", snap)
	}
	if snap.AvgLatencyMs != 100 {
		t.Fatalf("平均耗时 = %v, want 100", snap.AvgLatencyMs)
	}
	if snap.LocationRewrites != 2 || snap.HTMLHits != 7 {
		t.Fatalf("改写计数异常: %+v", snap)
	}
}

func TestSnapshotSorted(t *testing.T) {
	r := NewRegistry()
	r.For("C").Requests.Add(1)
	r.For("A").Requests.Add(1)
	r.For("B").Requests.Add(1)
	snaps := r.Snapshot()
	if len(snaps) != 3 || snaps[0].Service != "A" || snaps[2].Service != "C" {
		t.Fatalf("快照应按服务名排序: %+v", snaps)
	}
}

func TestTouchAndNilSafety(t *testing.T) {
	var nilReg *Registry
	if nilReg.For("A") != nil || nilReg.Snapshot() != nil {
		t.Fatal("nil 注册表应安全")
	}
	r := NewRegistry()
	r.Touch("A")
	if r.For("A").LastSeenUnix.Load() == 0 {
		t.Fatal("Touch 未记录时间")
	}
}

func TestLoopDetector(t *testing.T) {
	base := time.Now()
	d := NewLoopDetector(3, time.Second)
	d.now = func() time.Time { return base }

	for i := 0; i < 3; i++ {
		if d.Hit("A|1.2.3.4") {
			t.Fatalf("第 %d 次未越阈值却告警", i+1)
		}
	}
	if !d.Hit("A|1.2.3.4") {
		t.Fatal("第 4 次应越阈值告警")
	}
	if d.Hit("A|1.2.3.4") {
		t.Fatal("同一窗口内不应重复告警")
	}
	// 另一个来源独立计数
	if d.Hit("A|5.6.7.8") {
		t.Fatal("不同来源不应共享计数")
	}
	// 窗口滑过后重新计数
	base = base.Add(2 * time.Second)
	for i := 0; i < 3; i++ {
		if d.Hit("A|1.2.3.4") {
			t.Fatalf("新窗口第 %d 次不应告警", i+1)
		}
	}
	if !d.Hit("A|1.2.3.4") {
		t.Fatal("新窗口第 4 次应再次告警")
	}
}

func TestLoopDetectorDisabled(t *testing.T) {
	var nilDet *LoopDetector
	if nilDet.Hit("x") || nilDet.Len() != 0 {
		t.Fatal("nil 检测器应安全")
	}
	if NewLoopDetector(0, time.Second).Hit("x") {
		t.Fatal("阈值 0 视为禁用")
	}
}
