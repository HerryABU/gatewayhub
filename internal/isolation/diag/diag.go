// Package diag 提供隔离网关的诊断日志：每条日志带「责任分类」，
// 便于一眼区分「后端自己的 bug」「代理改写失败」「配置写错」（FR-18）。
package diag

import "log"

// Category 是日志的责任分类。
type Category string

const (
	// BackendBug 后端返回了非法/异常内容（非法 Cookie、异常重定向等）。
	BackendBug Category = "backend_bug"
	// RewriteFailed 代理侧改写失败（解压失败、体积超限、编码不支持）。
	RewriteFailed Category = "rewrite_failed"
	// ConfigError 配置错误（解析失败、字段非法、服务名冲突）。
	ConfigError Category = "config_error"
	// ProxyDegrade 代理主动降级（熔断打开、服务禁用）。
	ProxyDegrade Category = "proxy_degrade"
	// LoopWarning 疑似重定向循环。
	LoopWarning Category = "redirect_loop"
)

// Logf 输出一条带分类与服务的诊断日志。
func Logf(cat Category, service, format string, args ...any) {
	prefix := "[isolation][" + string(cat) + "]"
	if service != "" {
		prefix += "[" + service + "] "
	}
	log.Printf(prefix+format, args...)
}
