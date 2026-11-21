package rewrite

import "bytes"

// rewriteJS 只改写「资源类」根绝对路径（/assets、/static、/uploads…）。
//
// 为什么不改写 /api 字面量：JS 里 "/api/..." 既可能是请求 URL，也可能是
// **请求体数据**（NVS 的 /api/batch 就是 {path:"/api/nav-layout"} 这类结构）。
// 对它做字面量改写会污染业务数据（实测后端回 "path 必须以 /api/ 开头"，
// 11 个子请求全挂、顶部导航整条消失）。API 前缀改由注入的运行期 shim
// 在真正发请求时补（见 shim.go），静态层只碰确定的资源 URL。
func (rw *Rewriter) rewriteJS(b []byte) []byte {
	s := string(b)
	// 1) 单/双引号「以 / 开头」的字符串字面量（根相对资源路径）
	s = jsRootPathRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := jsRootPathRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		content := sub[2]
		if !isRootPath(content) {
			return m
		}
		return sub[1] + rw.rewritePath(content) + sub[3]
	})
	// 2) 反引号模板字面量（仅无 ${} 插值）
	s = jsTemplateRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := jsTemplateRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		if !isRootPath(sub[1]) {
			return m
		}
		return "`" + rw.rewritePath(sub[1]) + "`"
	})
	// 3) 整页导航：location.href = / assign( / replace( 的根相对路径
	s = locationRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := locationRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		return sub[1] + sub[2] + rw.rewritePath(sub[3]) + sub[4]
	})
	return []byte(s)
}

// needsCSSRewrite 快速预检：CSS 中是否可能包含需要重写的路径。
func needsCSSRewrite(b []byte) bool {
	return bytes.Contains(b, []byte("url(")) || bytes.Contains(b, []byte("@import"))
}

// needsJSRewrite 快速预检：JS 中是否可能包含需要重写的根相对路径。
// 先粗筛是否含 '/'（第三方库等多数无路径，单次扫描即返回），命中再逐前缀精确匹配，
// 避免对每个前缀做一次全量 bytes.Contains。
func needsJSRewrite(b []byte) bool {
	if !bytes.Contains(b, []byte("/")) {
		return false
	}
	for _, p := range rewriteRootPathPrefixes {
		if bytes.Contains(b, []byte(p)) {
			return true
		}
	}
	for _, p := range rewriteFilePrefixes {
		if bytes.Contains(b, []byte(p)) {
			return true
		}
	}
	return bytes.Contains(b, []byte("location.href")) ||
		bytes.Contains(b, []byte("location.assign")) ||
		bytes.Contains(b, []byte("location.replace"))
}
