package rewrite

import (
	"net/http"
	"testing"
)

func TestJoinChain(t *testing.T) {
	cases := []struct{ in, prefix, want string }{
		{"", "a", "a"},
		{"", "/a/", "a"},
		{"a", "b", "a/b"},
		{"a/b", "c", "a/b/c"},
		{"a/b", "", "a/b"},
	}
	for _, c := range cases {
		if got := JoinChain(c.in, c.prefix); got != c.want {
			t.Errorf("JoinChain(%q,%q)=%q want %q", c.in, c.prefix, got, c.want)
		}
	}
}

func TestSanitizeChainRejectsInjection(t *testing.T) {
	for _, raw := range []string{
		"../evil", "a/../b", "a//b", "a/b/", "a\"onmouseover=\"x", "javascript:alert(1)",
		"a/<script>", "a/中文", "a/" + string(make([]byte, 70)),
	} {
		if got := SanitizeChain(raw); got != "" {
			t.Errorf("SanitizeChain(%q) 应拒绝，实际 %q", raw, got)
		}
	}
	if got := SanitizeChain("Apps/Open-List_v2"); got != "apps/open-list_v2" {
		t.Fatalf("合法链应归一化小写: %q", got)
	}
}

func TestInboundChainRoundTrip(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://gw/a/b/c/", nil)
	req.Header.Set("X-GatewayHub-Hops", "1") // 只有网关自己的跳才信任链头
	if got := InboundChain(req); got != "" {
		t.Fatalf("新请求不应有链: %q", got)
	}
	WithInboundChain(req, "a/b")
	if got := InboundChain(req); got != "a/b" {
		t.Fatalf("链未写进 context: %q", got)
	}
}

func TestTrustedChainRequiresHopHeader(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://gw/a/", nil)
	req.Header.Set(PrefixChainHeader, "evil/path")
	if got := TrustedChain(req); got != "" {
		t.Fatalf("客户端伪造的链头不能被信任: %q", got)
	}
	req.Header.Set("X-GatewayHub-Hops", "2")
	if got := TrustedChain(req); got != "evil/path" {
		t.Fatalf("网关自己的跳应信任链头: %q", got)
	}
}

func TestRewriteLocationRejectsDangerousSchemes(t *testing.T) {
	rw := New("app", "", "backend:80")
	for _, loc := range []string{"javascript:alert(1)", "JAVASCRIPT:alert(1)", "data:text/html,x", "vbscript:msgbox(1)"} {
		if got := rw.rewriteLocation(loc); got != "/" {
			t.Errorf("危险 Location %q 应降级到 /，got %q", loc, got)
		}
	}
	if got := rw.rewriteLocation("https://example.com/x"); got != "https://example.com/x" {
		t.Errorf("外站 https 跳转应原样保留，got %q", got)
	}
}
