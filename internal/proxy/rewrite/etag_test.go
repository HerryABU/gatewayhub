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
