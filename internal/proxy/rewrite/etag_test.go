package rewrite

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func newResp(body string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", "text/html")
	h.Set("Content-Length", "1")
	h.Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
	h.Set("ETag", `"backend-tag"`)
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

// 打上指纹：ETag 换成本网关自算值，Cache-Control 必须是 no-cache，后段时间戳必须清掉。
func TestMarkRewrittenSetsOwnETag(t *testing.T) {
	resp := newResp("hello")
	body := []byte("hello")
	if MarkRewritten(nil, resp, body) {
		t.Fatal("没有 If-None-Match 时不应返回 304")
	}
	tag := resp.Header.Get("ETag")
	if tag == "" || tag == `"backend-tag"` {
		t.Fatalf("ETag 未替换为自算指纹: %q", tag)
	}
	if !strings.HasPrefix(tag, `"gw-`) {
		t.Errorf("ETag 应带 gw- 前缀以便区分本网关产物: %q", tag)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control 应为 no-cache（可存但必须再验证），实际 %q", cc)
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		t.Errorf("后端 Last-Modified 描述的是原文，必须删除，实际 %q", lm)
	}
	if resp.StatusCode != 200 {
		t.Errorf("不应改动状态码，实际 %d", resp.StatusCode)
	}
}

// 指纹相同 → 降级 304，且不得带 body/长度/类型。
func TestMarkRewrittenNotModified(t *testing.T) {
	resp := newResp("hello")
	body := []byte("hello")
	MarkRewritten(nil, resp, body)
	tag := resp.Header.Get("ETag")

	req, _ := http.NewRequest("GET", "http://gw/openlist/", nil)
	req.Header.Set("If-None-Match", tag)
	resp2 := newResp("hello")
	if !MarkRewritten(req, resp2, body) {
		t.Fatal("If-None-Match 命中时应返回 304")
	}
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("状态码应为 304，实际 %d", resp2.StatusCode)
	}
	if n, _ := io.ReadAll(resp2.Body); len(n) != 0 {
		t.Errorf("304 不得带 body，实际 %d 字节", len(n))
	}
	for _, h := range []string{"Content-Length", "Content-Type", "Content-Encoding"} {
		if v := resp2.Header.Get(h); v != "" {
			t.Errorf("304 不应带 %s，实际 %q", h, v)
		}
	}
}

// 内容变了（等价于网关改写逻辑升级）→ 指纹必须变，旧 If-None-Match 不得命中。
func TestMarkRewrittenTagTracksBody(t *testing.T) {
	a := newResp("")
	MarkRewritten(nil, a, []byte(`fetch("/api/x")`))
	b := newResp("")
	MarkRewritten(nil, b, []byte(`fetch("./api/x")`))
	if a.Header.Get("ETag") == b.Header.Get("ETag") {
		t.Fatal("不同字节必须得到不同 ETag，否则旧副本会继续命中 304")
	}
	req, _ := http.NewRequest("GET", "http://gw/", nil)
	req.Header.Set("If-None-Match", a.Header.Get("ETag"))
	c := newResp("")
	if MarkRewritten(req, c, []byte(`fetch("./api/x")`)) {
		t.Error("旧指纹不得命中新内容，否则客户端永远拿不到修复后的字节")
	}
}

func TestEtagMatches(t *testing.T) {
	tag := `"gw-abc"`
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{tag, true},
		{`"other"`, false},
		{`"other", ` + tag, true},
		{`W/` + tag, true},
		{"*", true},
		{"  " + tag + "  ", true},
	}
	for _, c := range cases {
		if got := etagMatches(c.in, tag); got != c.want {
			t.Errorf("etagMatches(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// 嵌套链：每一跳的 ETag 必须带自己的前缀，否则外层 If-None-Match 会在内层误命中 304。
func TestMarkRewrittenScopeIsHopLocal(t *testing.T) {
	body := []byte("same body after rewrite")
	a := newResp("")
	MarkRewrittenScope(nil, a, body, "a/b/c")
	b := newResp("")
	MarkRewrittenScope(nil, b, body, "c")
	if a.Header.Get("ETag") == b.Header.Get("ETag") {
		t.Fatalf("不同跳的 ETag 不能相同: %s", a.Header.Get("ETag"))
	}

	// 浏览器带着外层的 tag 回来，内层不得回 304（内层内容可能是未完全改写的版本）
	req, _ := http.NewRequest("GET", "http://gw/c/", nil)
	req.Header.Set("If-None-Match", a.Header.Get("ETag"))
	c := newResp("")
	if MarkRewrittenScope(req, c, body, "c") {
		t.Fatal("内层不得用外层的 ETag 命中 304")
	}

	// 同一跳、同一内容仍然可以正常 304
	req2, _ := http.NewRequest("GET", "http://gw/a/b/c/", nil)
	req2.Header.Set("If-None-Match", a.Header.Get("ETag"))
	d := newResp("")
	if !MarkRewrittenScope(req2, d, body, "a/b/c") {
		t.Fatal("同跳同内容应命中 304")
	}
}

func TestEtagScopeLongPrefixDoesNotCollide(t *testing.T) {
	a := strings.Repeat("a", 40) + "x"
	b := strings.Repeat("a", 40) + "y"
	if etagScope(a) == etagScope(b) {
		t.Fatal("长前缀截断后撞名，必须带原文 hash 兜底")
	}
	if s := etagScope("apps/openlist"); strings.ContainsAny(s, "/") || len(s) > 40 {
		t.Fatalf("scope 必须是安全 token: %q", s)
	}
}

// 兼容旧调用：无 scope 时仍是原来的 gw-<hash> 形态
func TestMarkRewrittenBackwardCompatible(t *testing.T) {
	old := newResp("")
	MarkRewritten(nil, old, []byte("x"))
	scoped := newResp("")
	MarkRewrittenScope(nil, scoped, []byte("x"), "")
	if old.Header.Get("ETag") != scoped.Header.Get("ETag") {
		t.Fatal("空 scope 应与旧函数行为一致")
	}
}
