package rewrite

import (
	"regexp"
	"strings"
)

// basePathCfgRe 匹配「运行期挂载点配置」中值为根路径 "/" 的字面量。
//
// 现代 SPA 普遍把挂载点放在页面内联配置里，供路由与请求库共用：
//
//	window.OPENLIST_CONFIG = { base_path: '/', ... }   // openlist / alist
//	window.__APP__ = { basePath: "/", basename: "/" }  // 常见 Vite/React 脚手架
//
// 后端按自身配置渲染成 "/"（即「我挂在站点根」）。当网关把它挂到 /{prefix} 下时，
// 浏览器实际访问的根不再是站点根 —— 这个值必须跟着改，否则 axios 拼出的
// "/api/..."、路由 basename、以及运行时拼的资源地址都会打到网关根路径上（404）。
//
// 只匹配「值恰好是 /」，不碰任何带内容的值（"/app"、"//cdn" 等），
// 因此不会误改业务数据；这与静态改写 JS 字面量（会污染 NVS /api/batch 请求体）有本质区别：
// 这里改的是「声明自己挂在哪」的元数据，改完与真实挂载点一致。
var basePathCfgRe = regexp.MustCompile(
	`(?i)(["']?(?:base_?path|basename|base_?url|public_?path|asset_?prefix|router_?base)["']?\s*:\s*)(["'])/(["'])`)

// rewriteBasePathConfig 把内联配置里的根挂载点改写为网关前缀，返回新内容与命中次数。
// 幂等：已是 /{prefix} 时正则不再匹配（值不是 "/"）。
func (rw *Rewriter) rewriteBasePathConfig(s string) (string, int) {
	p := rw.gatewayPath()
	if p == "/" || p == "" || s == "" {
		return s, 0
	}
	hits := 0
	out := basePathCfgRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := basePathCfgRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		hits++
		return sub[1] + sub[2] + p + sub[3]
	})
	return out, hits
}

// PrepareHTML 是「把后端站点挂到 /{prefix} 下」的响应侧准备，供路径隔离引擎复用：
//
//  1. 改写内联运行期配置里的挂载点（base_path/basename/baseURL … 值为 "/" 时 → "/{prefix}"）；
//  2. 注入运行期前缀兜底脚本（fetch/XHR/WS/导航/DOM 属性统一补前缀）。
//
// 两步都只作用于「网关转发的这一份 HTML」，后端文件与配置一个字节都不动，
// 等价于 nginx sub_filter / Apache mod_proxy_html 的做法，但更精确（只认识挂载点声明，
// 不做全文替换）。返回新内容与命中次数；prefix 为空时原样返回 0。
func PrepareHTML(prefix string, html []byte) ([]byte, int) {
	p := "/" + strings.Trim(prefix, "/")
	if p == "/" || len(html) == 0 {
		return html, 0
	}
	rw := &Rewriter{prefix: strings.Trim(prefix, "/")}
	s, hits := rw.rewriteBasePathConfig(string(html))
	if injected := rw.injectShim(s); injected != s {
		s = injected
		hits++
	}
	if hits == 0 {
		return html, 0
	}
	return []byte(s), hits
}
