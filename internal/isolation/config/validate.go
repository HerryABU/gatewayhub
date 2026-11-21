package config

import (
	"fmt"
	"net/http"
	"strings"
)

// reservedNames 是不允许被服务占用的首段：它们是网关自身（控制台/静态资源/管理 API）的入口。
// 一旦被服务名遮蔽，控制台与建站向导将无法访问。
var reservedNames = map[string]bool{
	"api":       true,
	"assets":    true,
	"static":    true,
	"favicon":   true,
	"isolation": true,
	"health":    true,
	"login":     true,
}

// validNameByte 判断服务名允许的字符：字母数字与 - _ . ~ （避免 URL 转义与 Cookie 名冲突）。
func validNameByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.', b == '~':
		return true
	}
	return false
}

// validateService 校验单条服务的语义约束（结构性问题已在 Build 中处理）。
func validateService(s *Service) error {
	if s.Name == "." || s.Name == ".." || strings.Contains(s.Name, "..") {
		return fmt.Errorf("%q: 服务名不能包含路径穿越片段", s.Name)
	}
	for i := 0; i < len(s.Name); i++ {
		if !validNameByte(s.Name[i]) {
			return fmt.Errorf("%q: 服务名含非法字符 %q（只允许字母数字与 - _ . ~）", s.Name, s.Name[i])
		}
	}
	if reservedNames[strings.ToLower(s.Name)] {
		return fmt.Errorf("%q: 服务名与网关自身路径保留字冲突（%s）", s.Name, strings.Join(reservedNameList(), ", "))
	}
	if s.CookieHat != "" && !validCookieToken(s.CookieHat) {
		return fmt.Errorf("%q: cookie_hat %q 含非法字符（必须能作为 Cookie 名前缀）", s.Name, s.CookieHat)
	}
	for _, p := range s.KnownAbsPrefix {
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
			return fmt.Errorf("%q: known_abs_prefixes 项 %q 必须以单个 / 开头", s.Name, p)
		}
	}
	if s.MaxRewriteBytes < 1024 {
		return fmt.Errorf("%q: max_rewrite_bytes 过小（%d），至少 1024", s.Name, s.MaxRewriteBytes)
	}
	return nil
}

// validCookieToken 判断字符串是否为合法的 Cookie 名（RFC 6265 token）。
// 直接用 http.Cookie 的校验做兜底，同时拒绝空串。
func validCookieToken(s string) bool {
	if s == "" {
		return false
	}
	c := &http.Cookie{Name: s, Value: "v"}
	return c.Valid() == nil
}

// Warnings 返回「能跑但可能踩坑」的配置提示（不影响启动）。
func Warnings(services []*Service) []string {
	var out []string
	for _, s := range services {
		if s.HTMLRewrite && !s.Mode.AllowsBodyRewrite() {
			out = append(out, fmt.Sprintf("%s: html_rewrite 已开启但 mode=%s 不改写响应体（仅 strip 模式生效）", s.Name, s.Mode))
		}
		if s.Mode == ModePassThrough && s.HTMLRewrite {
			out = append(out, fmt.Sprintf("%s: pass-through 模式下改写响应体会破坏「后端自己认前缀」的语义，建议关闭", s.Name))
		}
		if s.CookieHat == "" {
			out = append(out, fmt.Sprintf("%s: 未配置 cookie_hat，该服务的 Cookie 会与其他服务共享（无 Cookie 隔离）", s.Name))
		}
		if s.NoFullCompat {
			out = append(out, fmt.Sprintf("%s: 已标记不承诺完整兼容，代理只保证前缀归属/Cookie 隔离/重定向不越界", s.Name))
		}
	}
	if len(services) > 1 {
		// 帽子前缀互为前缀会造成「脱错帽」（A__ 是 A__x__ 的前缀），提前提示。
		for i := 0; i < len(services); i++ {
			for j := 0; j < len(services); j++ {
				if i == j || services[i].CookieHat == "" || services[j].CookieHat == "" {
					continue
				}
				if strings.HasPrefix(services[i].CookieHat, services[j].CookieHat) && services[i].CookieHat != services[j].CookieHat {
					out = append(out, fmt.Sprintf("cookie_hat 互为前缀：%s(%q) 与 %s(%q)，可能导致脱帽歧义",
						services[i].Name, services[i].CookieHat, services[j].Name, services[j].CookieHat))
				}
			}
		}
	}
	return out
}

// reservedNameList 返回排序后的保留字列表（用于错误信息，避免 map 顺序抖动）。
func reservedNameList() []string {
	return []string{"api", "assets", "favicon", "health", "isolation", "login", "static"}
}
