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
	if got := InboundChain(req); got != "" {
		t.Fatalf("新请求不应有链: %q", got)
	}
	WithInboundChain(req, "a/b")
	if got := InboundChain(req); got != "a/b" {
		t.Fatalf("链未写进 context: %q", got)
	}
}
