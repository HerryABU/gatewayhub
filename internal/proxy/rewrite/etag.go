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
	sum := sha256.Sum256(body)
	tag := `"gw-` + hex.EncodeToString(sum[:12]) + `"`
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
