// Package rewrite 负责「后端响应 → 网关可解析响应」的字面量改写。
//
// 拆包原则：这里只有纯函数式的字符串/字节处理与一次性的响应编排，
// 不感知服务配置、指标与熔断（那些属于路径隔离网关 engine）。
package rewrite

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// gwPathKey 用于把「网关侧原始请求路径」透传给 ModifyResponse（外链 CSS 相对化需要）。
type gwPathKey struct{}

// rewriter 把后端返回内容中的「根相对路径」改写为网关可解析的路径，
// 使依赖根目录的原始项目（SPA / 静态站）在 /{prefix}/... 下也能正常加载。
//
// 改写策略（「绝对改相对」）：
//   - HTML 属性 / 内联样式 / 内联脚本：根绝对路径 → 文档相对路径（"./x"），
//     配合无条件注入的 <base href="/{prefix}/">，由浏览器基于 base 解析，
//     任意深度部署 / 任意后端子路径（root）都能正确命中，不依赖网关前缀知识。
//   - 外链 CSS：url()/@import → 相对「CSS 文件自身位置」的路径（由网关侧 CSS 请求路径推算）。
//   - 外链 JS：API 前缀（/api）→ 文档相对（fetch/axios 按 document base 解析）；
//     资源前缀（/assets 等）→ 保留「前缀绝对」（import/new URL 按模块/脚本 URL 解析，相对化会错位）；
//     location 整页导航 → 保留「前缀绝对」（最稳）。
//   - Location 响应头：保留「前缀绝对」（重定向必须绝对）。
//
// Rewriter 把后端返回内容中的「根相对路径」改写为网关可解析的路径（前缀绝对/文档相对）。
//
// 改造说明（本包从 internal/proxy 拆出）：原来一个文件同时承担路径运算、HTML/CSS/JS
// 三类字面量改写、gzip 编解码与响应编排，属于典型的上帝类型；现在按职责拆成
// path.go（路径运算与判定）、html.go（HTML/CSS 字面量）、script.go（JS 字面量）、
// body.go（响应体编解码）与本文件（对外唯一的 Rewriter + 编排）。
type Rewriter struct {
	prefix string // 网关对外前缀，如 "java-order"
	root   string // 后端根路径，如 "" 或 "/api/v1"
	host   string // 后端 host，用于重写指向后端的绝对重定向
}

// New 创建改写器：prefix 为网关对外前缀（如 java-order），root 为后端根路径，host 为后端 host。
func New(prefix, root, host string) *Rewriter {
	return &Rewriter{prefix: prefix, root: root, host: host}
}

func (rw *Rewriter) gatewayPath() string {
	return "/" + rw.prefix
}

// rewritePath 把「以 / 开头」的根相对路径改写为网关子路径（前缀绝对形式）。
// 用于 Location 响应头、JS 资源引用（import / new URL / img.src 等，按模块/脚本 URL 解析）。

func (rw *Rewriter) Modify(resp *http.Response) error {
	// 嵌套链的完整对外前缀：优先用最内层写在响应头里的链；
	// 最内层自己则用「本跳收到的人站链 + 本跳前缀」拼出来。
	// 只有最外层（入站无链）才把内部头摘掉，不能让它漏给浏览器。
	inbound := SanitizeChain(InboundChain(resp.Request))
	full := SanitizeChain(resp.Header.Get(PrefixChainHeader))
	if full == "" {
		full = JoinChain(inbound, rw.prefix)
	}
	outermost := inbound == ""
	if outermost {
		defer resp.Header.Del(PrefixChainHeader) // 任何分支都不能把内部头漏给浏览器
	}
	rw = &Rewriter{prefix: full, root: rw.root, host: rw.host}

	// 重定向 Location（无需读 body）
	if loc := resp.Header.Get("Location"); loc != "" {
		resp.Header.Set("Location", rw.rewriteLocation(loc))
	}

	// Set-Cookie 的 Path 必须收紧到本服务前缀：同源下 Path=/ 的凭据会被
	// 发给兄弟前缀、并在对方页面的 DevTools 里可见（详见 cookie.go）。
	// 放在 MIME 判断之前：登录接口返回的 JSON 恰恰是下发 cookie 的地方。
	rw.tightenSetCookies(resp)

	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	isHTML := mt == "text/html" || mt == "application/xhtml+xml"
	isCSS := mt == "text/css"
	isJS := mt == "application/javascript" || mt == "text/javascript" ||
		mt == "application/x-javascript" || mt == "application/ecmascript" || mt == "text/ecmascript"
	if !isHTML && !isCSS && !isJS {
		return nil // 二进制 / JSON / 其他：直接透传
	}

	// 解不开的编码（br / zstd / 多段编码）必须一个字节都不碰：
	// 后面 writeBody 会按 enc 重打包，若 enc 判断错就会把压缩字节当明文发给浏览器。
	if _, ok := encodingOf(resp.Header.Get("Content-Encoding")); !ok {
		return nil
	}

	// 读全量判断：静态 import / 动态 import / fetch 路径声明可能位于文件任意位置，
	// 不能用有限窗口预检，否则 SPA 的路由懒加载 chunk（import 在文件中部）会被漏判导致 404。
	decoded, raw, enc, err := readBody(resp)
	if err != nil {
		// 读流失败时把已读到的字节还回去，不能让客户端收到被掏空的响应。
		if len(raw) > 0 {
			resp.Body = io.NopCloser(bytes.NewReader(raw))
		}
		return nil // 解压失败则原样透传
	}

	switch {
	case isHTML:
		// HTML 总是重写（至少注入 <base> 欺骗前端路由）
		decoded = rw.rewriteHTML(decoded)
	case isCSS:
		if !needsCSSRewrite(decoded) {
			resp.Body = io.NopCloser(bytes.NewReader(raw))
			return nil
		}
		// 外链 CSS：url()/@import 相对「CSS 文件自身位置」解析，需要网关侧 CSS 请求路径
		cssPath := ""
		if resp.Request != nil {
			cssPath, _ = resp.Request.Context().Value(gwPathKey{}).(string)
		}
		if cssPath != "" {
			decoded = rw.rewriteCSSFile(decoded, cssPath)
		} else {
			decoded = rw.rewriteCSS(decoded)
		}
	default: // JS
		if !needsJSRewrite(decoded) {
			resp.Body = io.NopCloser(bytes.NewReader(raw))
			return nil
		}
		decoded = rw.rewriteJS(decoded)
	}
	// 完整前缀链写回响应头，供外层继续改写（最外层由上面的 defer 摘掉）。
	if !outermost {
		resp.Header.Set(PrefixChainHeader, full)
	}

	// 体已改写：换成本网关自算的 ETag（no-cache 再验证），否则客户端可能长期复用旧副本。
	// 指纹必须带本跳前缀：嵌套链里每一跳都会改写同一份内容，ETag 若只取内容 hash，
	// 浏览器拿外层 ETag 去内层校验就会误命中 304，拿到外层已过时的一份字节。
	if MarkRewrittenScope(resp.Request, resp, decoded, rw.prefix) {
		return nil
	}
	writeBody(resp, decoded, enc)
	return nil
}

func (rw *Rewriter) rewriteLocation(loc string) string {
	if strings.HasPrefix(loc, "//") {
		return loc
	}
	if u, err := url.Parse(loc); err == nil && u.IsAbs() {
		if strings.EqualFold(u.Host, rw.host) {
			// 指向后端自身的绝对重定向，转为网关路径
			p := u.Path
			if u.RawQuery != "" {
				p += "?" + u.RawQuery
			}
			return rw.rewritePath(p)
		}
		return loc
	}
	if strings.HasPrefix(loc, "/") {
		return rw.rewritePath(loc)
	}
	return loc
}

// WithGWPath 在转发请求上记录网关侧原始路径（供外链 CSS 相对化推算基准目录）。
func WithGWPath(req *http.Request, gwPath string) *http.Request {
	ctx := context.WithValue(req.Context(), gwPathKey{}, gwPath)
	*req = *req.WithContext(ctx)
	return req
}
