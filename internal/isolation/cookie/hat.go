package cookie

import "strings"

// Hat 为 Cookie 名加上服务帽子前缀，如 ("A__", "session") → "A__session"。
// 已带帽的名字原样返回，避免重复加帽（后端自己写过带帽 Cookie 的幂等场景）。
func Hat(hat, name string) string {
	if hat == "" || name == "" {
		return name
	}
	if IsHatted(hat, name) {
		return name
	}
	return hat + name
}

// Unhat 去掉本服务帽子，返回新名与是否真的脱帽。
// 只脱「恰好以本服务帽子开头且剩余部分非空」的名字：宁可漏脱，也不误脱其他服务的帽子。
func Unhat(hat, name string) (string, bool) {
	if hat == "" || name == "" || !strings.HasPrefix(name, hat) {
		return name, false
	}
	rest := name[len(hat):]
	if rest == "" {
		return name, false // 只有帽子的非法名字，保持原样交给后端判断
	}
	return rest, true
}

// IsHatted 判断名字是否已带帽子前缀（剩余部分非空）。
func IsHatted(hat, name string) bool {
	return hat != "" && len(name) > len(hat) && strings.HasPrefix(name, hat)
}
