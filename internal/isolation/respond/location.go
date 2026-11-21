package respond

import (
	"net/url"
	"strings"

	"gatewayhub/internal/isolation/config"
)

// LocationOptions 是 Location 响应头修正参数（FR-10）。
type LocationOptions struct {
	Mode        config.Mode
	Prefix      string // 服务对外前缀，如 "/A"
	BackendHost string // 后端 host（含端口），用于识别「指向后端自身的绝对 URL」
	// FixPassThrough 仅对 pass-through 生效：后端未带前缀时是否补前缀。
	// 默认 false —— 保留模式应信任后端自己生成的路径。
	FixPassThrough bool
}

// RewriteLocation 修正重定向目标，保证浏览器不会跳出 /{服务名}/ 前缀。
//
// 处理分支（纯函数，相同输入必得相同输出）：
//  1. 指向后端自身的绝对 URL（http://127.0.0.1:9001/x）→ 改写为 /{服务名}/x，不泄露后端地址；
//  2. 指向其他站点的绝对 URL → 原样保留（代理无权改写外部跳转）；
//  3. 以 / 开头的根相对路径 → 剥离/隔离模式补前缀；保留模式按 FixPassThrough 决定；
//  4. 已经是 /{服务名}/... → 幂等不改；
//  5. 相对路径（home、../x）→ 原样保留（浏览器按当前 URL 解析）。
func RewriteLocation(loc string, opt LocationOptions) (string, bool) {
	raw := strings.TrimSpace(loc)
	if raw == "" {
		return loc, false
	}
	// 协议相对（//host/path）：先判定 host 是否为后端自身
	if strings.HasPrefix(raw, "//") {
		u, err := url.Parse("http:" + raw)
		if err != nil || u.Host == "" {
			return loc, false
		}
		if sameHost(u.Host, opt.BackendHost) {
			return applyPathFix(u, opt, loc)
		}
		return loc, false
	}

	if u, err := url.Parse(raw); err == nil && u.IsAbs() {
		if sameHost(u.Host, opt.BackendHost) {
			return applyPathFix(u, opt, loc)
		}
		return loc, false
	}

	if !strings.HasPrefix(raw, "/") {
		return loc, false // 相对路径 / 空路径：不猜测
	}
	fixed, changed := prefixPath(raw, opt)
	return fixed, changed
}

// applyPathFix 把「指向后端自身的绝对 URL」转成网关下的路径（丢弃后端 host，保留查询与片段）。
func applyPathFix(u *url.URL, opt LocationOptions, orig string) (string, bool) {
	p := u.Path
	if p == "" {
		p = "/"
	}
	fixed, _ := prefixPath(p, opt)
	if u.RawQuery != "" {
		fixed += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		fixed += "#" + u.Fragment
	}
	if fixed == orig {
		return orig, false
	}
	return fixed, true
}

// prefixPath 按模式为根相对路径补服务前缀。
func prefixPath(p string, opt LocationOptions) (string, bool) {
	if p == "" {
		return p, false
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return p, false
	}
	prefix := strings.TrimSuffix(opt.Prefix, "/")
	if prefix == "" {
		return p, false
	}
	if p == prefix || strings.HasPrefix(p, prefix+"/") {
		return p, false // 已带前缀，幂等
	}
	if !opt.Mode.StripsPrefix() && !opt.FixPassThrough {
		return p, false // 保留模式且不修正：信任后端
	}
	if p == "/" {
		return prefix + "/", true
	}
	return prefix + p, true
}

// sameHost 比较 host（大小写不敏感），忽略端口差异之外的任何猜测。
func sameHost(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
