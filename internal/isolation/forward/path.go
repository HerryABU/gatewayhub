package forward

import (
	"path"
	"strings"

	"gatewayhub/internal/isolation/config"
)

// Path 按服务模式计算「转发给后端的路径」（FR-5）。
//
//	rest 是匹配后剩余路径（"/A" → ""，"/A/" → "/"，"/A/admin" → "/admin"）：
//	  - pass-through：原样带上 /{服务名}，后端自己认前缀；
//	  - strip / isolated：剥离前缀，剩余为空时置为 "/"；
//	    并做 path.Clean 防止 `..` 越界，同时保留末尾斜杠语义。
func Path(mode config.Mode, prefix, rest string) string {
	if mode == config.ModePassThrough {
		if rest == "" {
			return prefix
		}
		return prefix + rest
	}
	if rest == "" || rest == "/" {
		return "/"
	}
	hadTrailing := strings.HasSuffix(rest, "/")
	cleaned := path.Clean("/" + strings.TrimPrefix(rest, "/"))
	if hadTrailing && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

// PrefixOf 返回服务对外前缀（"/" + 服务名）。
func PrefixOf(name string) string { return "/" + name }
