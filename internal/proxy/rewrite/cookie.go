package rewrite

import (
	"net/http"
	"strings"
)

// maxSetCookieHandled 单次响应处理的 Set-Cookie 条数上限。
// 正常后端不会超过个位数；超过说明响应头异常（或被拼错），多出的原样透传，
// 避免一个畸形响应把头部撑到几 MB。
const maxSetCookieHandled = 64

// expiredCookieValue 是「删除 cookie」用的过期时间（RFC 1123，1970-01-01）。
const expiredCookieValue = "Thu, 01 Jan 1970 00:00:00 GMT"

// tightenSetCookies 把响应里的 Set-Cookie 收紧到本服务的网关前缀下。
//
// 为什么需要：路径式反代下**所有服务同源**（同一个 scheme+host+port），而浏览器判断
// cookie 归属只看 (name, domain, path) —— 不看端口，也不看「这是谁的应用」。
// 后端只要把 Path 写成 "/"（极常见，如 NVS 的 token / refresh_token 就写死 "/"），
// 这个凭据就会：
//  1. 随兄弟前缀的请求一起发出去（/openlist/* 的每个请求都拖着 NVS 的 JWT）；
//  2. 在对方面页面的 DevTools → Application → Cookies 里原样可见；
//  3. 被对方后端（哪怕不认）读取到 Authorization 之外的会话标识。
//
// 这里把 Path 收到 /{prefix}，并补一条 Path=/ 的过期副本，把用户浏览器里
// **已经存在**的旧 cookie 删掉（同名的两份路径不同，浏览器视为两个 cookie，
// 不删旧的那份，收紧就只是给新增量打补丁，存量泄露照旧）。
//
// 只收紧 Path、**不改名**：普通代理链路没有请求侧的加帽逻辑，改名会让后端
// 自己读不到刚下发的会话（登录立刻失效）。需要名字级隔离请用隔离引擎的
// cookie_hat，或给服务配子域名。
func (rw *Rewriter) tightenSetCookies(resp *http.Response) {
	vals := resp.Header.Values("Set-Cookie")
	if len(vals) == 0 {
		return
	}
	base := rw.gatewayPath()
	if base == "/" || base == "" {
		return // 没有前缀可收紧（理论上不会发生）
	}

	out := make([]string, 0, len(vals)+2)
	for i, raw := range vals {
		if i >= maxSetCookieHandled {
			out = append(out, raw)
			continue
		}
		line, name, oldPath, domain, changed := rewriteCookiePath(raw, base, rw.root)
		if !changed {
			out = append(out, raw)
			continue
		}
		out = append(out, line)
		// 旧副本清理：仅当原本落在根路径（或压根没写 Path，RFC 默认取请求目录）
		if oldPath == "" || oldPath == "/" {
			out = append(out, clearCookie(name, domain))
		}
	}

	resp.Header.Del("Set-Cookie")
	for _, v := range out {
		resp.Header.Add("Set-Cookie", v)
	}
}

// rewriteCookiePath 改写单条 Set-Cookie 的 Path 属性。
// 返回改写后的整行、cookie 名、原 Path、Domain 与是否发生变化。
func rewriteCookiePath(raw, base, root string) (line, name, oldPath, domain string, changed bool) {
	segs := strings.Split(raw, ";")
	if len(segs) == 0 {
		return "", "", "", "", false
	}
	pair := strings.TrimSpace(segs[0])
	eq := strings.Index(pair, "=")
	if eq <= 0 {
		return "", "", "", "", false
	}
	name = strings.TrimSpace(pair[:eq])
	if name == "" {
		return "", "", "", "", false
	}

	pathIdx := -1
	for i := 1; i < len(segs); i++ {
		k, v := splitCookieAttr(segs[i])
		switch strings.ToLower(k) {
		case "path":
			pathIdx, oldPath = i, v
		case "domain":
			domain = v
		}
	}

	newPath := cookiePathFor(oldPath, base, root)
	if pathIdx >= 0 {
		if oldPath == newPath {
			return "", name, oldPath, domain, false
		}
		segs[pathIdx] = " Path=" + newPath
	} else {
		// 无 Path 属性时浏览器按「请求路径的目录」收窄，等价于当前页面下可见；
		// 显式补成服务前缀，语义不变但归属明确。
		segs = append(segs, "Path="+newPath)
	}
	return strings.Join(segs, ";"), name, oldPath, domain, true
}

// splitCookieAttr 把 " Path=/x " 拆成 ("Path", "/x")；无 '=' 属性的值为空。
func splitCookieAttr(s string) (key, val string) {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "=")
	if i < 0 {
		return s, ""
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
}

// cookiePathFor 把后端视角的 Path 归一到网关侧、并收进服务前缀：
//
//	("",     "/A")           → "/A"
//	("/",    "/A")           → "/A"
//	("/login","/A")          → "/A/login"
//	("/A/x", "/A")           → "/A/x"（已在前缀内，幂等）
//	("/api/x","/A",root="/api") → "/A/x"
func cookiePathFor(old, base, root string) string {
	p := strings.TrimSpace(old)
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if root = strings.TrimSuffix(strings.TrimSpace(root), "/"); root != "" {
		switch {
		case p == root:
			p = "/"
		case strings.HasPrefix(p, root+"/"):
			p = strings.TrimPrefix(p, root)
		}
	}
	switch {
	case p == "" || p == "/":
		return base
	case p == base || strings.HasPrefix(p, base+"/"):
		return p
	default:
		return base + p
	}
}

// clearCookie 生成一条把 (name, domain, "/") 这个旧副本删掉的 Set-Cookie。
// 只删根路径的那份，不影响同一响应该下发的 /{prefix} 新副本。
func clearCookie(name, domain string) string {
	var b strings.Builder
	b.Grow(len(name) + 96)
	b.WriteString(name)
	b.WriteString("=; Path=/; Max-Age=0; Expires=")
	b.WriteString(expiredCookieValue)
	if domain != "" {
		b.WriteString("; Domain=")
		b.WriteString(domain)
	}
	return b.String()
}
