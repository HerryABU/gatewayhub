package config

import "strings"

// Mode 是服务的处理模式，决定请求路径与响应体的处理方式。
//
//   - pass-through：原样透传 /{服务名}/...，后端自己认前缀，代理只做头层修正。
//   - strip：剥离 /{服务名} 前缀，后端以为自己挂在根路径；允许响应体字面量改写。
//   - isolated：剥离前缀 + 仅做 HTTP 头层修正，响应体一个字节都不改（降级模式）。
//
// 三种模式都是「纯函数」：输入（请求/响应 + 配置）确定，输出确定。
type Mode string

const (
	ModePassThrough Mode = "pass-through"
	ModeStrip       Mode = "strip"
	ModeIsolated    Mode = "isolated"
)

// modeAliases 收敛常见写法，避免配置里多一个下划线就整站 404。
var modeAliases = map[string]Mode{
	"pass-through": ModePassThrough,
	"passthrough":  ModePassThrough,
	"pass":         ModePassThrough,
	"transparent":  ModePassThrough,
	"strip":        ModeStrip,
	"prefix-strip": ModeStrip,
	"isolated":     ModeIsolated,
	"isolate":      ModeIsolated,
	"isolation":    ModeIsolated,
	"headers-only": ModeIsolated,
}

// ParseMode 解析模式字符串（大小写/连字符不敏感），返回是否合法。
func ParseMode(s string) (Mode, bool) {
	m, ok := modeAliases[strings.ToLower(strings.TrimSpace(s))]
	return m, ok
}

// Valid 判断模式是否合法。
func (m Mode) Valid() bool {
	return m == ModePassThrough || m == ModeStrip || m == ModeIsolated
}

// StripsPrefix 是否在转发前剥离 /{服务名} 前缀。
func (m Mode) StripsPrefix() bool { return m == ModeStrip || m == ModeIsolated }

// AllowsBodyRewrite 是否允许改写响应体。isolated 明确定义为「仅头层修正」，
// pass-through 语义是「后端自己认前缀」，两者都不改 body。
func (m Mode) AllowsBodyRewrite() bool { return m == ModeStrip }

// Canonical 返回规范写法（用于日志与 API 输出）。
func (m Mode) Canonical() string { return string(m) }

// HostMode 决定转发时 Host 头的取值（FR-8）。
type HostMode string

const (
	// HostModeKeep 保留浏览器侧的代理域名（大多数后端据此生成相对链接时更安全）。
	HostModeKeep HostMode = "keep"
	// HostModeRewrite 改写为后端 IP:端口（后端有基于 Host 的虚拟主机/鉴权时需要）。
	HostModeRewrite HostMode = "rewrite"
)

// ParseHostMode 解析 Host 策略，空值视为 keep。
func ParseHostMode(s string) (HostMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "keep", "preserve", "origin", "proxy":
		return HostModeKeep, true
	case "rewrite", "backend", "target":
		return HostModeRewrite, true
	}
	return "", false
}

// Canonical 返回规范写法。
func (h HostMode) Canonical() string { return string(h) }
