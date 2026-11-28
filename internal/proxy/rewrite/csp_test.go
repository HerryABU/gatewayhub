package rewrite

import (
	"net/http"
	"strings"
	"testing"
)

func TestExtractCSPNonce(t *testing.T) {
	h := http.Header{}
	h.Add("Content-Security-Policy", "script-src 'report-sample' data: 'nonce-5af715b8542000a557a661247e0e9c83-argus' 'strict-dynamic' 'self'")
	if got := extractCSPNonce(h); got != "5af715b8542000a557a661247e0e9c83-argus" {
		t.Errorf("提取 nonce 失败: %q", got)
	}
	h2 := http.Header{}
	h2.Add("Content-Security-Policy-Report-Only", "script-src 'nonce-abc123'")
	if got := extractCSPNonce(h2); got != "abc123" {
		t.Errorf("report-only nonce 失败: %q", got)
	}
	if got := extractCSPNonce(http.Header{}); got != "" {
		t.Errorf("无 CSP 应返回空: %q", got)
	}
}

func TestShimWithNonce(t *testing.T) {
	shim := `<script data-gateway-prefix-shim="1">(function(){})()</script>`
	got := shimWithNonce(shim, "abc123")
	if !strings.Contains(got, `<script nonce="abc123" data-gateway-prefix-shim="1">`) {
		t.Errorf("nonce 未注入: %s", got)
	}
	if shimWithNonce(shim, "") != shim {
		t.Error("空 nonce 不应改动 shim")
	}
}
