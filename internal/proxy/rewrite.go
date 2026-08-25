package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// rewriter 把后端返回内容中的「根相对路径」改写为网关子路径，
// 使依赖根目录的原始项目（SPA / 静态站）在 /{prefix}/... 下也能正常加载。
type rewriter struct {
	prefix string // 网关对外前缀，如 "java-order"
	root   string // 后端根路径，如 "" 或 "/api/v1"
	host   string // 后端 host，用于重写指向后端的绝对重定向
}

func newRewriter(prefix, root, host string) *rewriter {
	return &rewriter{prefix: prefix, root: root, host: host}
}

func (rw *rewriter) gatewayPath() string {
	return "/" + rw.prefix
}

// rewritePath 把「以 / 开头」的根相对路径改写为网关子路径。
func (rw *rewriter) rewritePath(raw string) string {
	if raw == "" || raw[0] != '/' || strings.HasPrefix(raw, "//") {
		return raw // 绝对 URL / 相对路径 / 协议相对，不处理
	}
	// 已带前缀则跳过，避免重复改写（如 /prefix/api/x）
	if raw == rw.gatewayPath() || strings.HasPrefix(raw, rw.gatewayPath()+"/") {
		return raw
	}
	root := strings.TrimSuffix(rw.root, "/")
	if root == "" {
		// 后端在根目录：/x → /prefix/x
		if raw == "/" {
			return rw.gatewayPath() + "/"
		}
		return rw.gatewayPath() + raw
	}
	// 后端在子路径：/{root}/x → /prefix/x
	if raw == root {
		return rw.gatewayPath()
	}
	if strings.HasPrefix(raw, root+"/") {
		return rw.gatewayPath() + strings.TrimPrefix(raw, root)
	}
	return raw
}

// rewriteRootPathPrefixes 是需要重写的根相对「资源 / 完整 API」路径前缀。
// 这些代表真实资源或完整 API 路径（如 /api/user、/assets/app.js、/static/logo.png），
// 而非 axios 的相对端点（如 /setup/env-check）。后者若被重写，会与 axios 的 baseURL
// 叠加成 /prefix/api/prefix/... 的双重前缀，故排除。
var rewriteRootPathPrefixes = []string{
	"/api", "/assets", "/static", "/uploads", "/media", "/images",
	"/img", "/fonts", "/css", "/js", "/public", "/dist", "/files",
}

// rewriteFilePrefixes 是带扩展名的常见资源前缀（精确匹配前缀，非路径段）。
var rewriteFilePrefixes = []string{
	"/favicon.", "/logo.", "/icon.", "/manifest.", "/robots.",
}

// isRootPath 判断字符串是否为需要重写的「根相对资源 / API 路径」。
func isRootPath(s string) bool {
	if len(s) < 2 || s[0] != '/' {
		return false
	}
	if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") {
		return false
	}
	for _, p := range rewriteRootPathPrefixes {
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	for _, p := range rewriteFilePrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// modify 作为 httputil.ReverseProxy.ModifyResponse，重写响应头与响应体。
// 性能设计（透明代理 + 逼近源站速度）：
//   - 只对 HTML/CSS/JS 文本做内容重写，图片/字体/视频等二进制直接透传。
//   - 预检用「先粗后细」扫描（先找 '/' 再找前缀），避免对每个前缀做全量扫描。
//   - 无需重写时用原始字节 raw 原样透传（不重新压缩），所有正则包级预编译。
func (rw *rewriter) modify(resp *http.Response) error {
	// 重定向 Location（无需读 body）
	if loc := resp.Header.Get("Location"); loc != "" {
		resp.Header.Set("Location", rw.rewriteLocation(loc))
	}

	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	isHTML := mt == "text/html" || mt == "application/xhtml+xml"
	isCSS := mt == "text/css"
	isJS := mt == "application/javascript" || mt == "text/javascript" ||
		mt == "application/x-javascript" || mt == "application/ecmascript" || mt == "text/ecmascript"
	if !isHTML && !isCSS && !isJS {
		return nil // 二进制 / JSON / 其他：直接透传
	}

	// 读全量判断：静态 import / 动态 import / fetch 路径声明可能位于文件任意位置，
	// 不能用有限窗口预检，否则 SPA 的路由懒加载 chunk（import 在文件中部）会被漏判导致 404。
	decoded, raw, gz, err := readBody(resp)
	if err != nil {
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
		decoded = rw.rewriteCSS(decoded)
	default: // JS
		if !needsJSRewrite(decoded) {
			resp.Body = io.NopCloser(bytes.NewReader(raw))
			return nil
		}
		decoded = rw.rewriteJS(decoded)
	}
	writeBody(resp, decoded, gz)
	return nil
}

func (rw *rewriter) rewriteLocation(loc string) string {
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

// ---- HTML 重写 ----

var (
	htmlAttrRe     = regexp.MustCompile(`(?i)(\b(?:src|href|action|poster|data-src|data-href|data-url)\s*=\s*["'])(/[^"'\s>]*)(["'])`)
	htmlSrcsetRe   = regexp.MustCompile(`(?i)(\b(?:srcset|data-srcset)\s*=\s*["'])([^"']*)(["'])`)
	metaRefreshRe  = regexp.MustCompile(`(?i)(<meta[^>]*content\s*=\s*["'])([^"]*url\s*=\s*/[^"]*)(["'])`)
	metaURLRe      = regexp.MustCompile(`(?i)(url\s*=\s*)(/[^"'\s]*)`)
	styleBlockRe   = regexp.MustCompile(`(?is)(<style[^>]*>)(.*?)(</style>)`)
	styleAttrDRe   = regexp.MustCompile(`(?i)(\bstyle\s*=\s*")([^"]*)(")`)
	styleAttrSRe   = regexp.MustCompile(`(?i)(\bstyle\s*=\s*')([^']*)(')`)
	cssURLRe       = regexp.MustCompile(`(?i)url\(\s*(['"]?)(/[^'")\s]+)(['"]?)\s*\)`)
	cssImportRe    = regexp.MustCompile(`(?i)(@import\s+)(["'])(/[^"']+)(["'])`)
	scriptBlockRe  = regexp.MustCompile(`(?is)(<script\b[^>]*>)(.*?)(</script>)`)
	scriptSrcRe    = regexp.MustCompile(`(?i)\bsrc\s*=`)
	// JS 中「以 / 开头」的字符串字面量（根相对路径）。只匹配这类，
	// 避免对每个字符串都回调判断，大幅减少大文件的重写开销。
	jsRootPathRe = regexp.MustCompile(`(["'])(/(?:[^"'\\]|\\.)*)(["'])`)
	// JS 模板字面量（无 ${} 插值的简单场景）
	jsTemplateRe = regexp.MustCompile("`(/[^`$]*)`")
	// JS 整页导航：location.href / assign / replace 的根相对路径
	locationRe = regexp.MustCompile(`(?i)(\blocation\.(?:href\s*=|assign\s*\(|replace\s*\()\s*)(["'])(/[^"'\s)]*)(["'])`)
	// HTML <head> 与 <base>：用于注入/改写 <base href>，欺骗前端路由基址
	headTagRe  = regexp.MustCompile(`(?i)(<head\b[^>]*>)`)
	baseHrefRe = regexp.MustCompile(`(?i)(<base\b[^>]*?\bhref\s*=\s*)(["'])([^"']*)(["'])`)
)

func (rw *rewriter) rewriteHTML(b []byte) []byte {
	s := string(b)

	// 1) srcset / data-srcset（逗号分隔的 URL 列表）
	s = htmlSrcsetRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlSrcsetRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		return sub[1] + rw.rewriteSrcset(sub[2]) + sub[3]
	})

	// 2) 常规 URL 属性
	s = htmlAttrRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlAttrRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		return sub[1] + rw.rewritePath(sub[2]) + sub[3]
	})

	// 3) <meta http-equiv="refresh"> 的 content="...url=/x"
	s = metaRefreshRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := metaRefreshRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		val := metaURLRe.ReplaceAllStringFunc(sub[2], func(mm string) string {
			ss := metaURLRe.FindStringSubmatch(mm)
			if len(ss) != 3 {
				return mm
			}
			return ss[1] + rw.rewritePath(ss[2])
		})
		return sub[1] + val + sub[3]
	})

	// 4) 内联 <style> 块与 style="" 属性内的 url(...)
	s = styleBlockRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := styleBlockRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		return sub[1] + string(rw.rewriteCSS([]byte(sub[2]))) + sub[3]
	})
	rewriteStyleAttr := func(re *regexp.Regexp) func(string) string {
		return func(m string) string {
			sub := re.FindStringSubmatch(m)
			if len(sub) != 4 {
				return m
			}
			return sub[1] + string(rw.rewriteCSS([]byte(sub[2]))) + sub[3]
		}
	}
	s = styleAttrDRe.ReplaceAllStringFunc(s, rewriteStyleAttr(styleAttrDRe))
	s = styleAttrSRe.ReplaceAllStringFunc(s, rewriteStyleAttr(styleAttrSRe))

	// 4.5) 内联 <script> 块（无 src 的），重写其中的根相对 API/资源路径
	s = scriptBlockRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := scriptBlockRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		if scriptSrcRe.MatchString(sub[1]) {
			return m // 外链 script 已由 htmlAttrRe 处理
		}
		return sub[1] + string(rw.rewriteJS([]byte(sub[2]))) + sub[3]
	})

	// 5) 注入 <base>，让前端路由器（Vue Router createWebHistory 等）以 /prefix/ 为基址
	s = rw.injectBase(s)

	return []byte(s)
}

// injectBase 注入或改写 <base href="/prefix/">，欺骗浏览器让前端路由器正确解析客户端路由。
// 仅在目标后端位于根目录（root 为空）时注入；带子路径的 API 后端跳过。
func (rw *rewriter) injectBase(s string) string {
	if strings.TrimSuffix(rw.root, "/") != "" {
		return s
	}
	baseHref := rw.gatewayPath() + "/"
	// 已有 <base>：仅改写其 href
	if baseHrefRe.MatchString(s) {
		return baseHrefRe.ReplaceAllStringFunc(s, func(m string) string {
			sub := baseHrefRe.FindStringSubmatch(m)
			if len(sub) != 5 {
				return m
			}
			return sub[1] + sub[2] + baseHref + sub[4]
		})
	}
	// 无 <base>：注入到 <head> 之后
	if headTagRe.MatchString(s) {
		return headTagRe.ReplaceAllStringFunc(s, func(m string) string {
			return m + "\n<base href=\"" + baseHref + "\">"
		})
	}
	// 无 <head>：注入到文档开头
	return "<base href=\"" + baseHref + "\">\n" + s
}

func (rw *rewriter) rewriteSrcset(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		sp := strings.IndexAny(p, " \t")
		var u, rest string
		if sp < 0 {
			u, rest = p, ""
		} else {
			u, rest = p[:sp], p[sp:]
		}
		parts[i] = rw.rewritePath(u) + rest
	}
	return strings.Join(parts, ", ")
}

func (rw *rewriter) rewriteCSS(b []byte) []byte {
	s := string(b)
	// url(...) 引用
	s = cssURLRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssURLRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		quote := sub[1]
		if quote == "" {
			quote = sub[3]
		}
		return "url(" + quote + rw.rewritePath(sub[2]) + quote + ")"
	})
	// @import "/x.css" 或 @import '/x.css'
	s = cssImportRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssImportRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		return sub[1] + sub[2] + rw.rewritePath(sub[3]) + sub[4]
	})
	return []byte(s)
}

// rewriteJS 重写 JS 里的根相对路径字符串（fetch/axios/import 等），
// 让依赖根目录的前端项目的 API 调用与动态分包也走 /prefix/... 子路径。
func (rw *rewriter) rewriteJS(b []byte) []byte {
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

// ---- 响应体读写（gzip 感知） ----

// readBody 读取响应体，返回解压后的内容（decoded）与原始字节（raw）。
// raw 用于「无需重写时原样透传」，避免无谓的解压/重压开销，保证响应速度。
func readBody(resp *http.Response) (decoded, raw []byte, gz bool, err error) {
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, false, err
	}
	enc := strings.ToLower(resp.Header.Get("Content-Encoding"))
	if strings.Contains(enc, "gzip") {
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, raw, true, err
		}
		defer gr.Close()
		decoded, err = io.ReadAll(gr)
		return decoded, raw, true, err
	}
	return raw, raw, false, nil
}

func writeBody(resp *http.Response, b []byte, gz bool) {
	var buf bytes.Buffer
	if gz {
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(b)
		_ = zw.Close()
		resp.Header.Set("Content-Encoding", "gzip")
	} else {
		resp.Header.Del("Content-Encoding")
		buf.Write(b)
	}
	resp.Body = io.NopCloser(&buf)
	resp.ContentLength = int64(buf.Len())
	resp.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
}
