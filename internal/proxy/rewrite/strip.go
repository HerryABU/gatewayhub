package rewrite

import (
	"net/http"
	"strings"
)

// applyStripRules 按配置删除响应头与 Set-Cookie。
// 用于：① 去掉上游无用的诊断头（X-WAF-UUID 等）；② 去掉会污染浏览器的临时 cookie
// （x-waf-* 挑战 cookie、上游内部会话）；③ 通用头裁剪。
// 只删不写、不碰 body——与 body 改写完全解耦，passthrough 模式下也能用。
func (rw *Rewriter) applyStripRules(resp *http.Response) {
	if resp == nil {
		return
	}
	// 删响应头
	for _, name := range rw.stripHeaders {
		if name == "" {
			continue
		}
		resp.Header.Del(http.CanonicalHeaderKey(name))
		// 非标准头（如 X-WAF-UUID）CanonicalHeaderKey 也能规范化，双保险用范围删
		for k := range resp.Header {
			if strings.EqualFold(k, name) {
				resp.Header.Del(k)
			}
		}
	}
	// 删 Set-Cookie：按名字前缀匹配（配置 "x-waf-" 匹配所有 x-waf-* cookie）
	if len(rw.stripCookies) == 0 {
		return
	}
	vals := resp.Header.Values("Set-Cookie")
	if len(vals) == 0 {
		return
	}
	kept := make([]string, 0, len(vals))
	for _, v := range vals {
		name := cookieName(v)
		drop := false
		for _, prefix := range rw.stripCookies {
			if prefix == "" {
				continue
			}
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, v)
		}
	}
	resp.Header.Del("Set-Cookie")
	for _, v := range kept {
		resp.Header.Add("Set-Cookie", v)
	}
}

// cookieName 从 Set-Cookie 值里取 cookie 名（等号前的部分）。
func cookieName(v string) string {
	if i := strings.IndexByte(v, '='); i > 0 {
		return strings.TrimSpace(v[:i])
	}
	return ""
}
