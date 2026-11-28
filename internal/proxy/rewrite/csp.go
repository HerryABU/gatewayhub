package rewrite

import (
	"net/http"
	"regexp"
	"strings"
)

// cspNonceRe 从 CSP 头里提取 script-src 的 nonce 值。
// 形如：script-src 'nonce-5af715...' 'strict-dynamic' ...
var cspNonceRe = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=_.-]+)'`)

// extractCSPNonce 优先从 Content-Security-Policy 取 nonce，
// 其次 Report-Only 头（有些站点只下发 report-only 的 nonce）。
func extractCSPNonce(h http.Header) string {
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		for _, v := range h.Values(name) {
			if m := cspNonceRe.FindStringSubmatch(v); len(m) == 2 {
				return m[1]
			}
		}
	}
	return ""
}

// shimWithNonce 给注入的 shim script 标签加 nonce 属性，使严格 CSP
// （script-src 'nonce-xxx' 'strict-dynamic'）允许执行。
func shimWithNonce(shim, nonce string) string {
	if nonce == "" || shim == "" {
		return shim
	}
	// 只改第一个标签（shim 就是单个 <script ...>...</script>）
	return strings.Replace(shim, "<script data-gateway-prefix-shim=\"1\">",
		"<script nonce=\""+nonce+"\" data-gateway-prefix-shim=\"1\">", 1)
}
