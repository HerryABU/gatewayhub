package rewrite

import "bytes"

func (rw *Rewriter) rewriteJS(b []byte) []byte {
	s := string(b)
	// 1) 单/双引号「以 / 开头」的字符串字面量（根相对路径）
	s = jsRootPathRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := jsRootPathRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		content := sub[2]
		if !isRootPath(content) {
			return m
		}
		if isAPIRootPath(content) {
			return sub[1] + rw.rewriteRelative(content) + sub[3]
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
		if isAPIRootPath(sub[1]) {
			return "`" + rw.rewriteRelative(sub[1]) + "`"
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
