package respond

import (
	"mime"
	"regexp"
	"strings"
)

// Kind 是需要做字面量改写的响应体类型。
type Kind int

const (
	KindOther Kind = iota
	KindHTML
	KindCSS
)

// DetectKind 依据 Content-Type 判定是否需要字面量改写。
// 只认 text/html（含 xhtml）与 text/css：JSON / JS / 图片 / 字体一律不碰（不保证覆盖 JS 运行时）。
func DetectKind(contentType string) Kind {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	switch strings.ToLower(mt) {
	case "text/html", "application/xhtml+xml":
		return KindHTML
	case "text/css":
		return KindCSS
	}
	return KindOther
}

// 只改写「带上下文的字面量」，避免误伤 JSON、注释、正文里的 "/xxx"：
//   - href="/  src="/  action="/
//   - url(/  url("/  url('/（CSS 与内联 style）
//
// 属性名前必须有空白：否则 `\bsrc=` 会连带命中 `data-src=`，而 `data-src` 是
// 「交给 JS 再拼一次基准」的延迟地址（vite legacy polyfill 就是
// `__dynamic_base__ + data-src`），被提前加前缀会拼成 /prefix/prefix/... 。
var (
	attrRe = regexp.MustCompile(`(?i)(\s(?:href|src|action)\s*=\s*)(["'])(/[^"'\s>]*)(["'])`)
	cssRe  = regexp.MustCompile(`(?i)(url\(\s*)(["']?)(/[^'")\s]*)(["']?)`)
)

// RewriteLiterals 为 HTML/CSS 中的根相对字面量补服务前缀，返回新内容与命中次数。
//
// 明确不做的事（与需求边界一致）：
//   - 不改写 JS 运行时拼接的路径（fetch('/api') / import('/chunk.js')）；
//   - 不改写协议相对（//cdn/x）与 data:/blob:/http(s):// 等非根相对地址；
//   - 不改写已带前缀的路径（幂等，后端自己生成 /{服务名}/x 时不会被二次加前缀）。
func RewriteLiterals(body []byte, kind Kind, prefix string) ([]byte, int) {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || len(body) == 0 || kind == KindOther {
		return body, 0
	}
	s := string(body)
	hits := 0
	if kind == KindHTML {
		s, hits = replaceAttr(s, prefix, hits)
	}
	s, hits = replaceCSS(s, prefix, hits)
	if hits == 0 {
		return body, 0
	}
	return []byte(s), hits
}

// replaceAttr 处理 href/src/action 属性值（保留原始引号与其余字符）。
func replaceAttr(s, prefix string, hits int) (string, int) {
	out := attrRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := attrRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		fixed, changed := addPrefix(sub[3], prefix)
		if !changed {
			return m
		}
		hits++
		return sub[1] + sub[2] + fixed + sub[4]
	})
	return out, hits
}

// replaceCSS 处理 url(...)（引号可缺省，可混合）。
func replaceCSS(s, prefix string, hits int) (string, int) {
	out := cssRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		fixed, changed := addPrefix(sub[3], prefix)
		if !changed {
			return m
		}
		hits++
		quote := sub[2]
		if quote == "" {
			quote = sub[4]
		}
		return sub[1] + quote + fixed + quote
	})
	return out, hits
}

// addPrefix 为根相对路径补前缀；已经带前缀 / 协议相对 / 非根相对一律原样返回。
func addPrefix(p, prefix string) (string, bool) {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return p, false
	}
	if p == prefix || strings.HasPrefix(p, prefix+"/") {
		return p, false
	}
	return prefix + p, true
}
