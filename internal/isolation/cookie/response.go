package cookie

import "strings"

// SetCookieOptions 是响应侧 Set-Cookie 修正参数。
type SetCookieOptions struct {
	// Hat 是本服务的 Cookie 帽子前缀；为空表示不做 Cookie 隔离（原样放行）。
	Hat string
	// Prefix 是服务对外前缀，如 "/A"，用于收紧 Path。
	Prefix string
	// BackendRoot 是后端挂载根路径（如 "/api"）：后端按自身视角写出的 Path
	// 需要先归一到「剥离后的路径」再加网关前缀。
	BackendRoot string
	// PassInvalid 为 true 时非法 Cookie 原样透传（默认丢弃并记录）。
	PassInvalid bool
}

// SetCookieResult 描述一次响应侧修正结果。
type SetCookieResult struct {
	Values    []string // 修正后的 Set-Cookie 列表（与输入一一对应，丢弃项已移除）
	Rewritten int      // 实际被加帽/收紧 Path 的条数
	Invalid   []string // 非法（Cookie 名不合法）的原始值，供日志定位后端 bug
}

// RewriteSetCookies 逐个修正 Set-Cookie（FR-11）：
//   - 多值逐条处理，绝不合并或丢弃同名的多个 Cookie；
//   - 属性（HttpOnly/Secure/SameSite/Expires/Max-Age/Domain…）原样保留，顺序不变；
//   - 名字加帽子前缀；Path=/ 或缺失的 Path 收紧为 /{服务名}；
//   - 非法 Cookie 按策略透传或丢弃，但一定记录。
func RewriteSetCookies(values []string, opt SetCookieOptions) SetCookieResult {
	res := SetCookieResult{Values: make([]string, 0, len(values))}
	if opt.Hat == "" {
		// 未配置帽子 = 明确关闭 Cookie 隔离，响应头不动（避免半吊子改写）
		res.Values = append(res.Values, values...)
		return res
	}
	for _, raw := range values {
		name, rest, attrs, ok := parseSetCookie(raw)
		if !ok || !validToken(name) {
			res.Invalid = append(res.Invalid, raw)
			if opt.PassInvalid {
				res.Values = append(res.Values, raw)
			}
			continue
		}
		newName := Hat(opt.Hat, name)
		changed := newName != name

		pathIdx := -1
		for i, a := range attrs {
			if attrKey(a) == "path" {
				pathIdx = i
				break
			}
		}
		defaultPath := opt.Prefix
		if pathIdx >= 0 {
			oldPath := attrValue(attrs[pathIdx])
			newPath := tightenPath(oldPath, opt)
			if newPath != oldPath {
				attrs[pathIdx] = "Path=" + newPath
				changed = true
			}
		} else {
			attrs = append(attrs, "Path="+defaultPath)
			changed = true
		}

		if changed {
			res.Rewritten++
		}
		var b strings.Builder
		b.WriteString(newName)
		b.WriteString(rest)
		for _, a := range attrs {
			b.WriteString("; ")
			b.WriteString(a)
		}
		res.Values = append(res.Values, b.String())
	}
	return res
}

// parseSetCookie 把一条 Set-Cookie 拆成 name、value 片段（含 '='）与属性列表。
// 手写而非 http.ParseSetCookie：需要逐字保留未识别属性（如 Partitioned）与原始顺序。
func parseSetCookie(raw string) (name, valuePart string, attrs []string, ok bool) {
	segs := splitAttrs(raw)
	if len(segs) == 0 {
		return "", "", nil, false
	}
	first := segs[0]
	i := strings.Index(first, "=")
	if i <= 0 {
		return "", "", nil, false
	}
	name = strings.TrimSpace(first[:i])
	if name == "" {
		return "", "", nil, false
	}
	valuePart = first[i:]
	if len(segs) > 1 {
		attrs = append(attrs, segs[1:]...)
	}
	return name, valuePart, attrs, true
}

// splitAttrs 按 ';' 切分并忽略引号内的分号；每段去首尾空白。
func splitAttrs(raw string) []string {
	out := make([]string, 0, 8)
	var cur strings.Builder
	inQuote := false
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ';' && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// attrKey 返回属性的小写键名（"Path=/x" → "path"，"HttpOnly" → "httponly"）。
func attrKey(attr string) string {
	if i := strings.Index(attr, "="); i >= 0 {
		return strings.ToLower(strings.TrimSpace(attr[:i]))
	}
	return strings.ToLower(strings.TrimSpace(attr))
}

// attrValue 返回属性值（去引号、去空白）。
func attrValue(attr string) string {
	i := strings.Index(attr, "=")
	if i < 0 {
		return ""
	}
	v := strings.TrimSpace(attr[i+1:])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return v
}

// tightenPath 把后端给出的 Path 收紧到服务前缀下，并归一到「网关侧路径」。
//
//	("", "/A")           → "/A"
//	("/", "/A")          → "/A"
//	("/login", "/A")     → "/A/login"
//	("/A/x", "/A")       → "/A/x"（已带前缀，幂等）
//	("/api/x", "/A", root="/api") → "/A/x"
func tightenPath(p string, opt SetCookieOptions) string {
	p = strings.TrimSpace(p)
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	root := strings.TrimSuffix(opt.BackendRoot, "/")
	if root != "" {
		if p == root {
			p = "/"
		} else if strings.HasPrefix(p, root+"/") {
			p = strings.TrimPrefix(p, root)
		}
	}
	prefix := strings.TrimSuffix(opt.Prefix, "/")
	if prefix == "" {
		return p
	}
	switch {
	case p == "" || p == "/":
		return prefix
	case p == prefix || strings.HasPrefix(p, prefix+"/"):
		return p
	default:
		return prefix + p
	}
}

// validToken 判断字符串是否为合法的 Cookie 名（RFC 6265 token），
// 与 http.Cookie 的校验一致，但允许作为前缀拼接。
func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7f {
			return false
		}
		switch c {
		case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=', '{', '}':
			return false
		}
	}
	return true
}
