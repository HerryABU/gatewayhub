package engine

import (
	"net/http"

	"gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/cookie"
	"gatewayhub/internal/isolation/diag"
	"gatewayhub/internal/isolation/metrics"
)

// cookieRewrite 是请求侧脱帽（FR-6）：只认识本服务的帽子，别家帽子原样保留或按策略丢弃。
func cookieRewrite(req *http.Request, cfg *config.Service) cookie.RequestResult {
	if cfg.CookieHat == "" {
		return cookie.RequestResult{}
	}
	return cookie.RewriteRequest(req.Header, cfg.CookieHat, cfg.DropForeign)
}

// rewriteCookies 是响应侧加帽 + Path 收紧（FR-11）。
// 多值逐条处理；只有真的发生改写才重建 header，避免无谓的 header 抖动。
func (rt *runtime) rewriteCookies(resp *http.Response, cfg *config.Service, cnt *metrics.Counters) {
	vals := resp.Header.Values("Set-Cookie")
	if len(vals) == 0 || cfg.CookieHat == "" {
		return
	}
	res := cookie.RewriteSetCookies(vals, cookie.SetCookieOptions{
		Hat:         cfg.CookieHat,
		Prefix:      cfg.Prefix,
		BackendRoot: cfg.Root,
	})
	if len(res.Invalid) > 0 {
		diag.Logf(diag.BackendBug, cfg.Name, "后端返回 %d 条非法 Set-Cookie（%q…），已按策略处理: %s",
			len(res.Invalid), preview(res.Invalid[0]), requestPath(resp))
	}
	if res.Rewritten == 0 && len(res.Invalid) == 0 && len(res.Values) == len(vals) {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, v := range res.Values {
		resp.Header.Add("Set-Cookie", v)
	}
	if res.Rewritten > 0 {
		cnt.CookieRewrites.Add(int64(res.Rewritten))
	}
}

// preview 截断过长的日志片段，避免一条非法 Cookie 把日志撑爆。
func preview(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
