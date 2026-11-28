package rewrite

import (
	"regexp"
	"strings"
)

// ---- HTML 重写 ----

var (
	// 属性名前必须有空白（不能用 \b：`data-src=` 里 src 前是 '-'，\b 成立会被误命中）。
	// 延迟地址（data-src / data-srcset 等）会被前端再拼一次基准
	//（vite legacy polyfill：__dynamic_base__ + data-src），提前改写会拼成 /prefix/prefix/...。
	htmlAttrRe = regexp.MustCompile(`(?i)(\s(?:src|href|action|poster)\s*=\s*["'])(/[^"'\s>]*)(["'])`)
	// HTML5 允许属性值不加引号（example.com 就写 `<script src=/s.js>`）。
	// 旧实现只认带引号的，这类根绝对路径会原样透传 → 浏览器按站点根解析，
	// 请求打到网关根路径而不是 /prefix/，拿回 SPA 的 HTML 当 JS 执行，
	// 页面报 "SyntaxError: Unexpected token '<'"。无引号值以空白或 > 结束。
	htmlAttrBareRe = regexp.MustCompile(`(?i)(\s(?:src|href|action|poster)\s*=\s*)(/[^\s>"']+)`)
	htmlSrcsetRe   = regexp.MustCompile(`(?i)(\ssrcset\s*=\s*["'])([^"']*)(["'])`)
	metaRefreshRe  = regexp.MustCompile(`(?i)(<meta[^>]*content\s*=\s*["'])([^"]*url\s*=\s*/[^"]*)(["'])`)
	metaURLRe      = regexp.MustCompile(`(?i)(url\s*=\s*)(/[^"'\s]*)`)
	styleBlockRe   = regexp.MustCompile(`(?is)(<style[^>]*>)(.*?)(</style>)`)
	styleAttrDRe   = regexp.MustCompile(`(?i)(\bstyle\s*=\s*")([^"]*)(")`)
	styleAttrSRe   = regexp.MustCompile(`(?i)(\bstyle\s*=\s*')([^']*)(')`)
	cssURLRe       = regexp.MustCompile(`(?i)url\(\s*(['"]?)(/[^'")\s]+)(['"]?)\s*\)`)
	cssImportRe    = regexp.MustCompile(`(?i)(@import\s+)(["'])(/[^"']+)(["'])`)
	scriptBlockRe  = regexp.MustCompile(`(?is)(<script\b[^>]*>)(.*?)(</script>)`)
	scriptSrcRe    = regexp.MustCompile(`(?i)\bsrc\s*=`)
	// 外站 script 的 crossorigin/integrity：
	// 反代 SPA（DeepSeek 实测）把页面挂在 /dps/ 下后，外站 CDN 上的 <script
	// crossorigin integrity="sha384-…" src="https://fe-static.deepseek.com/...">
	// 会发起 CORS 请求；CDN 返回 ACAO: https://*.deepseek.com，浏览器 origin 是网关
	// 域名 → 被拒 → 页面卡在「资源加载异常」（但 HTTP 200）。去掉 crossorigin 让
	// 浏览器按经典跨域脚本加载（不做 CORS），同时必须去掉 integrity —— 跨域无 CORS
	// 时 SRI 校验会失败、脚本同样不执行。只针对 https:// 的外站 script。
	crossOriginAttrRe = regexp.MustCompile(`(?i)\s+crossorigin(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?`)
	integrityAttrRe   = regexp.MustCompile(`(?i)\s+integrity\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
	externalScriptRe  = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']https://[^"']+["'][^>]*>`)
	moduleScriptRe    = regexp.MustCompile(`(?i)\btype\s*=\s*["']?module\b`)
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
	// 2b) 无引号属性：同样绝对改相对（example.com 的 <script src=/s.js> 就靠这里）
	s = htmlAttrBareRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlAttrBareRe.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		return sub[1] + rw.rewriteRelative(sub[2])
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

	// 4.4) 外站 script 去 crossorigin/integrity：让经典跨域脚本按浏览器默认策略加载，
	//      避免 CDN 的 ACAO 白名单把网关 origin 拒掉（DeepSeek 实测：200 但白屏）。
	//      type=module 强制 CORS，去掉 crossorigin 也没用，原样放行。
	s = externalScriptRe.ReplaceAllStringFunc(s, func(m string) string {
		if moduleScriptRe.MatchString(m) {
			return m
		}
		m = crossOriginAttrRe.ReplaceAllString(m, "")
		return integrityAttrRe.ReplaceAllString(m, "")
	})

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

	// 4.6) 运行期挂载点声明（base_path / basename / baseURL = "/"）：改写为网关前缀。
	//     SPA 的路由 basename、axios baseURL、运行时资源前缀都从这里取值，
	//     不改这一处，前两者会在浏览器端拼出根路径（实测 openlist：白屏 + 全部 API 404）。
	s, baseHits := rw.rewriteBasePathConfig(s)

	// 5) 注入 <base>，让前端路由器（Vue Router createWebHistory 等）与文档相对路径以 /prefix/ 为基址。
	//    仅在应用**没有**自述挂载点时注入：已经改写了 base_path 的应用自己就知道挂在哪，
	//    再叠一层 <base> 会让 `baseURL`/`new URL(x, baseURI)` 各拼一次 → /prefix/prefix/...。
	if baseHits == 0 {
		s = rw.injectBase(s)
	}

	// 6) 注入运行期前缀兜底脚本（fetch/XHR/WS/DOM 属性统一补前缀）。
	//    它承担「JS 字面量不能乱改」的那部分职责：只改请求，不改数据。
	s = rw.injectShim(s)

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

// rewriteChallengePaths 只给 WAF 挑战页的 HTML 属性（src/href/action/poster）
// 补网关前缀，让 probe.js 等探针资源能被加载；不注入 shim、不碰脚本内容。
// 挑战页必须原样执行探针 JS 才能通过校验——任何额外改写都会破坏 WAF 验证。
func rewriteChallengePaths(b []byte, prefix string) []byte {
	if prefix == "" || prefix == "/" {
		return b
	}
	s := string(b)
	// prefix 可能无前导斜杠（SanitizeChain 规范化后是 "qidian"），
	// 但 HTML 属性里的路径必须以 / 开头，否则浏览器按相对路径解析到站点根。
	prefix = "/" + strings.Trim(prefix, "/")
	ps := prefix + "/"
	s = htmlAttrRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlAttrRe.FindStringSubmatch(m)
		if len(sub) != 4 {
			return m
		}
		return sub[1] + ps + strings.TrimPrefix(sub[2], "/") + sub[3]
	})
	s = htmlAttrBareRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := htmlAttrBareRe.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		return sub[1] + ps + strings.TrimPrefix(sub[2], "/")
	})
	return []byte(s)
}
