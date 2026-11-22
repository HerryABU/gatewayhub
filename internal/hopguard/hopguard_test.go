package hopguard

import (
	"net/http"
	"strconv"
	"testing"
)

func TestNextIncrements(t *testing.T) {
	g := New(3)
	h := http.Header{}

	n, ok := g.Next(h)
	if !ok || n != 1 {
		t.Fatalf("首跳：want 1,true got %d,%v", n, ok)
	}
	Stamp(h, n)

	n, ok = g.Next(h)
	if !ok || n != 2 {
		t.Fatalf("第二跳：want 2,true got %d,%v", n, ok)
	}
	Stamp(h, n)

	n, ok = g.Next(h)
	if !ok || n != 3 {
		t.Fatalf("第三跳（到上限）：want 3,true got %d,%v", n, ok)
	}
	Stamp(h, n)

	n, ok = g.Next(h)
	if ok {
		t.Fatalf("第四跳应被拒（上限 3），实际放行 n=%d", n)
	}
	if n != 4 {
		t.Fatalf("拒绝时应返回 4 便于日志定位，got %d", n)
	}
}

func TestNextInvalidValuesTreatedAsStart(t *testing.T) {
	g := New(5)
	for _, raw := range []string{"", "abc", "-2", "999", "1e3", "  ", "1000000000000000000000"} {
		h := http.Header{}
		h.Set(Header, raw)
		n, ok := g.Next(h)
		if !ok || n != 1 {
			t.Fatalf("入站值 %q 应视为起点 1，got %d,%v", raw, n, ok)
		}
	}
}

func TestNextAcceptsMultiValueFirst(t *testing.T) {
	g := New(5)
	h := http.Header{}
	h.Set(Header, "2, 7")
	n, ok := g.Next(h)
	if !ok || n != 3 {
		t.Fatalf("多值应取第一个：want 3,true got %d,%v", n, ok)
	}
}

func TestNextCeilingNotAccumulatedFromForgedHugeValue(t *testing.T) {
	// 伪造超过硬上限的值：归一为 0，请求还能正常走一跳，
	// 不会因为一个外部头把自家链路卡死。
	g := New(5)
	h := http.Header{}
	h.Set(Header, strconv.Itoa(ceiling+1))
	n, ok := g.Next(h)
	if !ok || n != 1 {
		t.Fatalf("超硬上限应归零，got %d,%v", n, ok)
	}
}

func TestDefaultMax(t *testing.T) {
	for _, max := range []int{0, -1} {
		if got := New(max).Max(); got != DefaultMax {
			t.Fatalf("New(%d).Max() = %d, want %d", max, got, DefaultMax)
		}
	}
	var nilGuard *Guard
	if nilGuard.Max() != DefaultMax {
		t.Fatal("nil Guard 应返回 DefaultMax（避免调用方额外判空）")
	}
}

func TestNilHeaderIsStart(t *testing.T) {
	g := New(2)
	n, ok := g.Next(nil)
	if !ok || n != 1 {
		t.Fatalf("nil header 应视为起点，got %d,%v", n, ok)
	}
	Stamp(nil, 9) // 不应 panic
}
