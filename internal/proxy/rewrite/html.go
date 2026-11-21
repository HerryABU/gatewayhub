package rewrite

import (
	"regexp"
	"strings"
)

// ---- HTML 重写 ----

var (
	htmlAttrRe    = regexp.MustCompile(`(?i)(\b(?:src|href|action|poster|data-src|data-href|data-url)\s*=\s*["'])(/[^"'\s>]*)(["'])`)
	htmlSrcsetRe  = regexp.MustCompile(`(?i)(\b(?:srcset|data-srcset)\s*=\s*["'])([^"']*)(["'])`)
	metaRefreshRe = regexp.MustCompile(`(?i)(<meta[^>]*content\s*=\s*["'])([^"]*url\s*=\s*/[^"]*)(["'])`)
	metaURLRe     = regexp.MustCompile(`(?i)(url\s*=\s*)(/[^"'\s]*)`)
	styleBlockRe  = regexp.MustCompile(`(?is)(<style[^>]*>)(.*?)(</style>)`)
	styleAttrDRe  = regexp.MustCompile(`(?i)(\bstyle\s*=\s*")([^"]*)(")`)
	styleAttrSRe  = regexp.MustCompile(`(?i)(\bstyle\s*=\s*')([^']*)(')`)
	cssURLRe      = regexp.MustCompile(`(?i)url\(\s*(['"]?)(/[^'")\s]+)(['"]?)\s*\)`)
	cssImportRe   = regexp.MustCompile(`(?i)(@import\s+)(["'])(/[^"']+)(["'])`)
	scriptBlockRe = regexp.MustCompile(`(?is)(<script\b[^>]*>)(.*?)(</script>)`)
	scriptSrcRe   = regexp.MustCompile(`(?i)\bsrc\s*=`)
	// JS 中「以 / 开头」的字符串字面量（根相对路径）。只匹配这类，
	// 避免对每个字符串都回调判断，大幅减少大文件的重写开销。
	jsRootPathRe = regexp.MustCompile(`(["'])(/(?:[^"'\\]|\\.)*)(["'])`)
	// JS 模板字面量（无 ${} 插值的简单场景）
	jsTemplateRe = regexp.MustCompile("`(/[^`$]*)`")
	// JS 整页导航：location.href / assign / replace 的根相对路径
	locationRe = regexp.MustCompile(`(?i)(\blocation\.(?:href\s*=|assign\s*\(|replace\s*\()\s*)(["'])(/[^"'\s)]*)(["'])`)
	// HTML <head> 与 <base>：用于注入/改写 <base href>，欺骗前端路由器基址
	headTagRe  = regexp.MustCompile(`(?i)(<head\b[^>]*>)`)
	baseHrefRe = regexp.MustCompile(`(?i)(<base\b[^>]*?\bhref\s*=\s*)(["'])([^"']*)(["'])`)
)

func (rw *Rewriter) rewriteHTML(b []byte) []byte {
	s := string(b)

	// 1) srcset / data-srcset（逗号分隔的 URL 列表）
	s = htmlSrcsetRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlSrcsetRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		return sub[1] + rw.rewriteSrcset(sub[2]) + sub[3]
	})

	// 2) 常规 URL 属性：绝对改相对（"./x"），由 <base> 兜底解析
	s = htmlAttrRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlAttrRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		return sub[1] + rw.rewriteRelative(sub[2]) + sub[3]
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
			return ss[1] + rw.rewriteRelative(ss[2])
		})
		return sub[1] + val + sub[3]
	})

	// 4) 内联 <style> 块与 style="" 属性内的 url(...)：相对文档 base，同样绝对改相对
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

	// 5) 注入 <base>，让前端路由器（Vue Router createWebHistory 等）与文档相对路径以 /prefix/ 为基址。
	//    无论后端是否位于子路径（root）都注入——相对路径解析统一依赖它。
	s = rw.injectBase(s)

	return []byte(s)
}

// injectBase 注入或改写 <base href="/prefix/">。始终注入（不区分后端 root）。
func (rw *Rewriter) injectBase(s string) string {
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

func (rw *Rewriter) rewriteSrcset(v string) string {
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
		parts[i] = rw.rewriteRelative(u) + rest
	}
	return strings.Join(parts, ", ")
}

// rewriteCSS 内联样式版：url()/@import 根绝对 → 文档相对（内联样式按 document base 解析）。
func (rw *Rewriter) rewriteCSS(b []byte) []byte {
	return rw.rewriteCSSWith(b, rw.rewriteRelative)
}

// rewriteCSSFile 外链 CSS 版：url()/@import 根绝对 → 相对「CSS 文件自身位置」（cssPath 为网关侧请求路径）。
func (rw *Rewriter) rewriteCSSFile(b []byte, cssPath string) []byte {
	return rw.rewriteCSSWith(b, func(p string) string {
		return rw.relativeToFile(p, cssPath)
	})
}

func (rw *Rewriter) rewriteCSSWith(b []byte, conv func(string) string) []byte {
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
		return "url(" + quote + conv(sub[2]) + quote + ")"
	})
	// @import "/x.css" 或 @import '/x.css'
	s = cssImportRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssImportRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		return sub[1] + sub[2] + conv(sub[3]) + sub[4]
	})
	return []byte(s)
}

// rewriteJS 重写 JS 里的根相对路径字符串：
//   - API 前缀（/api...）：相对化（fetch/axios 按 document base 解析，配合 <base>）
//   - 资源前缀（/assets 等）：保留「前缀绝对」（import / new URL / img.src 按模块/脚本 URL 解析，相对化会错位）
//   - 整页导航 location.*：保留「前缀绝对」
