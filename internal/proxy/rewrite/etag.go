package rewrite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
)

// MarkRewritten 给「已被网关改写」的响应打上内容指纹与再验证策略，返回是否已降级为 304。
//
// 为什么必须打：改写后的字节与后端原文不同，后端给的 ETag/Last-Modified 描述的是**原文**。
// 只把它们删掉（不做别的）会留下一个危险空档 —— 响应没有任何校验器，浏览器就按启发式规则
// 长期缓存，而网关的改写逻辑是会升级的。一旦升级，用户浏览器仍握着旧产物不放。
//
// 实测事故（v1.5.x → 真机复现）：旧版网关把 JS 里的 "/api" 字面量改写成 "./api"，
// Edge 把这份畸形 JS 缓存在 transferSize=0 的位置永久复用；网关修好之后，用户浏览器
// 依然用旧副本，app 把接口拼成 /openlist./api/...，全部落空，页面只剩
// 「获取设置失败： Undefined」。同一 URL 换 Chrome / 换新源（cpolar）就正常 —— 因为没那份缓存。
//
// 做法：用改写后的字节算强 ETag，配 Cache-Control: no-cache（可存，但每次必须再验证）；
// 请求带了匹配的 If-None-Match 就直接回 304，既省流量又保证「网关一改，客户端立刻跟上」。
func MarkRewritten(req *http.Request, resp *http.Response, body []byte) bool {
	return MarkRewrittenScope(req, resp, body, "")
}

// MarkRewrittenScope 与 MarkRewritten 相同，但把 scope（网关前缀 / 隔离服务名）
// 编进 ETag。嵌套反代里同一份内容会被多个跳各自改写，若 tag 只取内容 hash，
// 外层浏览器带来的 If-None-Match 会在内层误命中 304 —— 内层直接把外层尚未改写的
// 版本当成"没变"回掉，浏览器就永远停在上一跳的产物上（资源前缀、<base> 都是错的）。
// 每个跳用自己的 scope，条件请求只在生成它的那一跳命中。
func MarkRewrittenScope(req *http.Request, resp *http.Response, body []byte, scope string) bool {
	sum := sha256.Sum256(body)
	tag := `"gw-` + etagScope(scope) + hex.EncodeToString(sum[:12]) + `"`
	resp.Header.Set("ETag", tag)
	// 可缓存但必须再验证：网关改写逻辑升级后，指纹变化会让旧副本自然失效
	resp.Header.Set("Cache-Control", "no-cache")
	// 后端的时间戳描述的是原文，留着会让条件请求永远命中旧体
	resp.Header.Del("Last-Modified")

	if req == nil || !etagMatches(req.Header.Get("If-None-Match"), tag) {
		return false
	}
	resp.StatusCode = http.StatusNotModified
	resp.Status = "304 Not Modified"
	resp.Body = io.NopCloser(bytes.NewReader(nil))
	resp.ContentLength = 0
	resp.Header.Del("Content-Length")
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-MD5")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Del("Content-Type")
	return true
}

// etagScope 把前缀归一成 ETag token 可用的字符，并限长，避免多层前缀拼出超长头。
func etagScope(scope string) string {
	s := strings.ToLower(strings.Trim(scope, "/"))
	if s == "" {
		return ""
	}
	const keep = 24
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= keep {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('.')
		}
	}
	// 长前缀不能只截断：两个不同的长前缀可能截断成同一个 ETag 命名空间。
	// 超出部分用原文 hash 兜底，保证不同跳的 tag 必然不同。
	if len(s) > keep {
		sum := sha256.Sum256([]byte(s))
		b.WriteByte('-')
		b.WriteString(hex.EncodeToString(sum[:4]))
	} else {
		b.WriteByte('.')
	}
	return b.String()
}

// etagMatches 判断请求的 If-None-Match 是否命中给定指纹。
// 支持逗号分隔多值、弱校验前缀 W/ 与通配 *。
func etagMatches(ifNoneMatch, tag string) bool {
	ifNoneMatch = strings.TrimSpace(ifNoneMatch)
	if ifNoneMatch == "" {
		return false
	}
	if ifNoneMatch == "*" {
		return true
	}
	want := strings.TrimPrefix(tag, "W/")
	for _, part := range strings.Split(ifNoneMatch, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == want {
			return true
		}
	}
	return false
}
