package config

import (
	"fmt"
	"strings"
	"time"

	"gatewayhub/internal/target"
)

// 默认值集中定义：配置缺省时行为可预测，不依赖散落的魔法数字。
const (
	DefaultTimeout         = 10 // 秒
	DefaultMaxRewriteBytes = 2 << 20
	DefaultCookieHatSuffix = "__"
	DefaultBreakerFailures = 5
	DefaultBreakerCooldown = 30 // 秒
	DefaultLoopThreshold   = 8
	DefaultLoopWindow      = 10 // 秒
)

// Service 是校验并填充默认值后的服务条目（运行时只读快照）。
// 它由 Spec + Defaults 推导而来，绝不直接在运行时修改（NFR-1 配置只读）。
type Service struct {
	Name            string        `json:"name"`
	Prefix          string        `json:"prefix"` // "/" + Name
	Target          string        `json:"target"`
	Backend         target.Parsed `json:"-"` // 解析后的后端地址（不对外暴露 IP:端口，NFR-5）
	Scheme          string        `json:"scheme"`
	Host            string        `json:"-"`    // 后端 host，仅日志/审计使用
	Root            string        `json:"root"` // 后端根路径
	Mode            Mode          `json:"mode"`
	CookieHat       string        `json:"cookie_hat"`
	HTMLRewrite     bool          `json:"html_rewrite"`
	KnownAbsPrefix  []string      `json:"known_abs_prefixes"`
	Timeout         int           `json:"timeout"`
	HostMode        HostMode      `json:"host_mode"`
	NoFullCompat    bool          `json:"no_full_compat"`
	MaxRewriteBytes int64         `json:"max_rewrite_bytes"`
	DropForeign     bool          `json:"drop_foreign_cookies"`
	PassThroughFix  bool          `json:"passthrough_location_prefix"`
	Enabled         bool          `json:"enabled"`
}

// TimeoutDuration 返回转发超时。
func (s *Service) TimeoutDuration() time.Duration {
	if s.Timeout <= 0 {
		return DefaultTimeout * time.Second
	}
	return time.Duration(s.Timeout) * time.Second
}

// BodyRewriteEnabled 响应体改写是否真正生效：服务开关 + 模式允许 + 服务启用。
func (s *Service) BodyRewriteEnabled() bool {
	return s.Enabled && s.HTMLRewrite && s.Mode.AllowsBodyRewrite()
}

// Defaults 是服务条目的全局默认值。
type Defaults struct {
	Mode            string `yaml:"mode"`
	Timeout         int    `yaml:"timeout"`
	HostMode        string `yaml:"host_mode"`
	MaxRewriteBytes int64  `yaml:"max_rewrite_bytes"`
	CookieHatSuffix string `yaml:"cookie_hat_suffix"`
	// DropForeignCookies 为 true 时，请求里「不属于本服务帽子」的 Cookie 一律丢弃。
	DropForeignCookies bool `yaml:"drop_foreign_cookies"`
}

// BreakerOptions 单服务熔断参数（FR/NFR-4）。
type BreakerOptions struct {
	Failures int `yaml:"failures"` // 连续失败阈值，<=0 表示禁用熔断
	Cooldown int `yaml:"cooldown"` // 熔断冷却秒数，冷却后进入半开试探
}

// RedirectLoopOptions 重定向循环告警参数（FR-17）。
type RedirectLoopOptions struct {
	Threshold int `yaml:"threshold"` // 时间窗内同一来源的 3xx 次数上限
	Window    int `yaml:"window"`    // 时间窗（秒）
}

// Spec 是 YAML 中一条服务声明的原始形态（允许缺省，由 Defaults 兜底）。
type Spec struct {
	Name              string   `yaml:"name"`
	Target            string   `yaml:"target"`
	Mode              string   `yaml:"mode"`
	CookieHat         *string  `yaml:"cookie_hat"` // 显式空串 = 不加帽；不写 = 用 name+后缀
	HTMLRewrite       bool     `yaml:"html_rewrite"`
	KnownAbsPrefixes  []string `yaml:"known_abs_prefixes"`
	Timeout           int      `yaml:"timeout"`
	HostMode          string   `yaml:"host_mode"`
	NoFullCompat      bool     `yaml:"no_full_compat"`
	MaxRewriteBytes   int64    `yaml:"max_rewrite_bytes"`
	DropForeignCookie *bool    `yaml:"drop_foreign_cookies"`
	PassThroughFix    bool     `yaml:"passthrough_location_prefix"`
	Enabled           *bool    `yaml:"enabled"`
}

// File 是 isolation.yaml 的完整结构（显式配置表，FR-2）。
type File struct {
	Version      int                 `yaml:"version"`
	Defaults     Defaults            `yaml:"defaults"`
	Breaker      BreakerOptions      `yaml:"breaker"`
	RedirectLoop RedirectLoopOptions `yaml:"redirect_loop"`
	Services     []Spec              `yaml:"services"`
}

// normalizeDefaults 把缺省项填成与内置默认一致（避免零值语义歧义）。
func (f *File) normalizeDefaults() {
	d := &f.Defaults
	if strings.TrimSpace(d.Mode) == "" {
		d.Mode = string(ModeIsolated) // 最保守：默认只做头层隔离
	}
	if d.Timeout <= 0 {
		d.Timeout = DefaultTimeout
	}
	if strings.TrimSpace(d.HostMode) == "" {
		d.HostMode = string(HostModeKeep)
	}
	if d.MaxRewriteBytes <= 0 {
		d.MaxRewriteBytes = DefaultMaxRewriteBytes
	}
	if d.CookieHatSuffix == "" {
		d.CookieHatSuffix = DefaultCookieHatSuffix
	}
	if f.Breaker.Failures == 0 {
		f.Breaker.Failures = DefaultBreakerFailures
	}
	if f.Breaker.Cooldown <= 0 {
		f.Breaker.Cooldown = DefaultBreakerCooldown
	}
	if f.RedirectLoop.Threshold <= 0 {
		f.RedirectLoop.Threshold = DefaultLoopThreshold
	}
	if f.RedirectLoop.Window <= 0 {
		f.RedirectLoop.Window = DefaultLoopWindow
	}
}

// Build 把 File 解析成运行时服务快照。所有校验问题一次性聚合返回（不因首条错误中断），
// 便于运维一次改完（FR-18「配置错误」可定位）。
func (f *File) Build() ([]*Service, error) {
	f.normalizeDefaults()
	var issues []string
	seen := make(map[string]int, len(f.Services))
	out := make([]*Service, 0, len(f.Services))

	for i, sp := range f.Services {
		svc := f.buildOne(sp, i, &issues)
		if svc == nil {
			continue
		}
		key := strings.ToLower(svc.Name)
		if prev, dup := seen[key]; dup {
			issues = append(issues, fmt.Sprintf("services[%d] %q: 服务名重复（与 services[%d] 冲突）", i, svc.Name, prev))
			continue
		}
		seen[key] = i
		out = append(out, svc)
	}
	if len(issues) > 0 {
		return nil, &Issues{Items: issues}
	}
	return out, nil
}

// buildOne 解析并校验单条服务；返回 nil 表示该条不可用（错误已追加到 issues）。
func (f *File) buildOne(sp Spec, idx int, issues *[]string) *Service {
	d := f.Defaults
	bad := func(format string, args ...any) *Service {
		*issues = append(*issues, fmt.Sprintf("services[%d]: %s", idx, fmt.Sprintf(format, args...)))
		return nil
	}

	name := strings.TrimSpace(sp.Name)
	name = strings.Trim(name, "/")
	if name == "" {
		return bad("缺少 name（路径第一段，如 A）")
	}

	modeRaw := sp.Mode
	if strings.TrimSpace(modeRaw) == "" {
		modeRaw = d.Mode
	}
	mode, ok := ParseMode(modeRaw)
	if !ok {
		return bad("%q: 未知 mode %q（可选 pass-through / strip / isolated）", name, sp.Mode)
	}

	hostRaw := sp.HostMode
	if strings.TrimSpace(hostRaw) == "" {
		hostRaw = d.HostMode
	}
	hostMode, ok := ParseHostMode(hostRaw)
	if !ok {
		return bad("%q: 未知 host_mode %q（可选 keep / rewrite）", name, sp.HostMode)
	}

	tgt := strings.TrimSpace(sp.Target)
	if tgt == "" {
		return bad("%q: 缺少 target（后端地址，如 10.0.0.5:8080 或 :8080）", name)
	}
	parsed, err := target.ParseBackend(tgt)
	if err != nil {
		return bad("%q: target 解析失败: %v", name, err)
	}

	timeout := sp.Timeout
	if timeout <= 0 {
		timeout = d.Timeout
	}
	maxBody := sp.MaxRewriteBytes
	if maxBody <= 0 {
		maxBody = d.MaxRewriteBytes
	}

	dropForeign := d.DropForeignCookies
	if sp.DropForeignCookie != nil {
		dropForeign = *sp.DropForeignCookie
	}
	enabled := true
	if sp.Enabled != nil {
		enabled = *sp.Enabled
	}

	svc := &Service{
		Name:            name,
		Prefix:          "/" + name,
		Target:          tgt,
		Backend:         parsed,
		Scheme:          parsed.Scheme,
		Host:            parsed.Host,
		Root:            parsed.Root,
		Mode:            mode,
		CookieHat:       resolveHat(sp.CookieHat, name, d.CookieHatSuffix),
		HTMLRewrite:     sp.HTMLRewrite,
		KnownAbsPrefix:  cleanPrefixes(sp.KnownAbsPrefixes),
		Timeout:         timeout,
		HostMode:        hostMode,
		NoFullCompat:    sp.NoFullCompat,
		MaxRewriteBytes: maxBody,
		DropForeign:     dropForeign,
		PassThroughFix:  sp.PassThroughFix,
		Enabled:         enabled,
	}

	if err := validateService(svc); err != nil {
		*issues = append(*issues, err.Error())
		return nil
	}
	return svc
}

// resolveHat 决定 Cookie 帽子前缀：显式配置优先；未配置时用 name + 后缀；
// 显式空串表示明确「不加帽」（不做 Cookie 隔离）。
func resolveHat(explicit *string, name, suffix string) string {
	if explicit != nil {
		return strings.TrimSpace(*explicit)
	}
	return name + suffix
}

// cleanPrefixes 去空白、去重、去掉空项，保留声明顺序。
func cleanPrefixes(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Issues 是聚合的配置错误集合（一次性反馈全部问题）。
type Issues struct {
	Items []string
}

func (e *Issues) Error() string {
	if len(e.Items) == 1 {
		return "配置错误: " + e.Items[0]
	}
	return fmt.Sprintf("配置错误（%d 项）: %s", len(e.Items), strings.Join(e.Items, "; "))
}
