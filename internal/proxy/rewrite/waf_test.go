package rewrite

import (
	"net/http"
	"strings"
	"testing"
)

// TestIsWAFChallenge WAF 反爬挑战页（202 + X-WAF-UUID）必须原样透传，
// 否则注入的 shim/改写会破坏 probe.js 挑战 → 站点白屏。
func TestIsWAFChallenge(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header http.Header
		want   bool
	}{
		{"起点挑战页", 202, http.Header{"X-Waf-Uuid": []string{"627563..."}}, true},
		{"Lego Server", 202, http.Header{"Server": []string{"Lego Server"}}, true},
		{"其它 X-WAF 头", 202, http.Header{"X-Waf-Captcha-Referer": []string{""}}, true},
		{"202 但无 WAF 特征", 202, http.Header{}, false},
		{"200 正常页带 WAF 头", 200, http.Header{"X-Waf-Uuid": []string{"x"}}, false},
		{"nil", 0, nil, false},
	}
	for _, c := range cases {
		resp := &http.Response{StatusCode: c.status, Header: c.header}
		if got := isWAFChallenge(resp); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// TestRewriteChallengePaths 挑战页只补 src/href 前缀，不注入 shim、不改脚本内容。
func TestRewriteChallengePaths(t *testing.T) {
	in := []byte(`<!DOCTYPE html><html><head><script>var buid="fffffffffffffffffff"</script><script src="/C2WF946J0/probe.js?v=vc1jasc"></script></head><body></body></html>`)
	out := string(rewriteChallengePaths(in, "/qidian"))
	for _, want := range []string{
		`src="/qidian/C2WF946J0/probe.js?v=vc1jasc"`, // 探针路径已补前缀
		`var buid="fffffffffffffffffff"`,             // 内联脚本原样
	} {
		if !strings.Contains(out, want) {
			t.Errorf("挑战页改写缺少 %q\n实际: %s", want, out)
		}
	}
	if strings.Contains(out, "data-gateway-prefix-shim") || strings.Contains(out, "__GATEWAY_PREFIX__") {
		t.Error("挑战页不能注入 shim")
	}
	// 无前导斜杠的 prefix（SanitizeChain 规范化后）也要补成 /qidian/...
	if got := string(rewriteChallengePaths(in, "qidian")); !strings.Contains(got, `src="/qidian/C2WF946J0/probe.js`) {
		t.Errorf("无斜杠 prefix 未补前导 /: %s", got)
	}
	// 根前缀/空前缀不改
	for _, p := range []string{"", "/"} {
		if got := string(rewriteChallengePaths(in, p)); got != string(in) {
			t.Errorf("prefix=%q 不应改动", p)
		}
	}
}
