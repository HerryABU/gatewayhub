package rewrite

import (
	"bytes"
	"regexp"
	"strings"
)

// rewriteJS 只改写「整页导航」这一类确定的 URL 用法（location.href = / assign( / replace(）。
//
// 为什么不改写其它 JS 字面量：JS 里以 "/" 开头的字符串**无法从文本上判断用途**。
// 已经被真实踩中两次：
//   - NVS：`/api/batch` 的请求体是 {id,method,path}，path 值形如 "/api/nav-layout"，
//     被改写成 "./api/nav-layout" 后后端回 "path 必须以 /api/ 开头"，11 个子请求全挂、
//     顶部导航整条消失；
//   - openlist：接口写成 `$.get(\`/public/plugins\`)`，而 "/public" 看起来像静态资源目录，
//     被加上前缀后与 axios baseURL 叠加成 /openlist/api/openlist/public/plugins。
//
// 这类碰撞无法用「可信前缀白名单」穷举，只能不在文本层猜。
//
// 因此策略是：静态层只碰 HTML 属性 / CSS / 整页导航；真正发出去的请求
// （fetch / XHR / sendBeacon / EventSource / WebSocket / pushState）与 DOM 的 src/href
// 全部由注入的运行期 shim（shim.go）在发起时补前缀 —— 只碰请求，不碰数据。
// assetImportRe 匹配 `import("/assets/chunk.js")` 与 `new URL("/assets/x.css", import.meta.url)`。
//
// 这两处是唯一可以安全静态改写的 JS 资源加载形式：
//   - 它们的第一个参数在语法上**必定是模块/资源 URL**，不可能是请求体数据
//     （这正是不能乱改 `fetch("/api/x")` 与 `fetch("/api/batch")` 的原因）；
//   - Vite 的代码分割与 __vitePreload 全靠这两个形式拉 chunk，漏改就是首屏白屏。
//
// 仍只认根绝对路径 `/...`：相对路径由浏览器按 document base / 模块 URL 解析，协议相对
// `//cdn/...` 是外站资源，都不能碰。
var assetImportRe = regexp.MustCompile(
	`(?i)(\b(?:import\s*\(\s*|new\s+URL\s*\(\s*))(["'` + "`" + `])(/[^"'` + "`" + `\s)]+)(["'` + "`" + `])`)

func (rw *Rewriter) rewriteJS(b []byte) []byte {
	s := string(b)
	s = locationRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := locationRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		return sub[1] + sub[2] + rw.rewritePath(sub[3]) + sub[4]
	})
	// Vite 懒加载 chunk / 预加载依赖：根绝对 → 带网关前缀的绝对路径。
	// 用 rewritePath 而非 rewriteRelative：import/new URL 按模块 URL 解析，
	// 文档相对（./x）会错位（历史注释里"资源前缀保留前缀绝对"说的就是这件事）。
	s = assetImportRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := assetImportRe.FindStringSubmatch(m)
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

// needsJSRewrite 快速预检：JS 中是否出现「整页导航」写法。
// 只认这三处，绝大多数第三方库与业务代码直接跳过（大文件零成本）。
func needsJSRewrite(b []byte) bool {
	return bytes.Contains(b, []byte("location.href")) ||
		bytes.Contains(b, []byte("location.assign")) ||
		bytes.Contains(b, []byte("location.replace")) ||
		containsImportCall(b)
}

// containsImportCall 低成本预检：动态 import 或 new URL 首参看起来像根绝对资源。
// 只做字节扫描，避免大 JS 为每个文件都跑正则。
func containsImportCall(b []byte) bool {
	if !bytes.Contains(b, []byte("import(")) && !bytes.Contains(b, []byte("new URL(")) {
		return false
	}
	s := string(b)
	for _, marker := range []string{`import("`, `import('`, "import(`", `new URL("`, `new URL('`, "new URL(`"} {
		if i := strings.Index(s, marker); i >= 0 {
			rest := s[i+len(marker):]
			if strings.HasPrefix(rest, "/") && !strings.HasPrefix(rest, "//") {
				return true
			}
		}
	}
	return false
}
