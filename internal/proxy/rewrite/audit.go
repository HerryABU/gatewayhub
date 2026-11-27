package rewrite

import (
	"context"
	"net/http"
)

// Audit 是一次响应改写的审计结果。
//
// 放在请求 context 里（而不是响应头里）传回调用方：响应头是发给浏览器的，
// 内部审计信息既不该泄露，也会在链路上被层层覆盖。
type Audit struct {
	Hits   int
	Chain  string // 完整对外前缀链
	Kind   string // html / css / js
	Skip   string // too_large / unsupported_encoding / decode_failed / no_hit
	Status int
}

type auditKey struct{}

// WithAudit 把审计载体挂到请求上（原地修改，与 WithGWPath 同一模式）。
func WithAudit(req *http.Request, a *Audit) *http.Request {
	if req == nil || a == nil {
		return req
	}
	*req = *req.WithContext(context.WithValue(req.Context(), auditKey{}, a))
	return req
}

// AuditOf 取回审计载体；没有挂载时返回 nil（调用方需判空）。
func AuditOf(req *http.Request) *Audit {
	if req == nil {
		return nil
	}
	a, _ := req.Context().Value(auditKey{}).(*Audit)
	return a
}

// MarkSkip 记录跳过原因（首次生效，避免后续分支覆盖根因）。
func (a *Audit) MarkSkip(reason string) {
	if a != nil && a.Skip == "" {
		a.Skip = reason
	}
}

// MarkHit 累加改写命中数并记录类型。
func (a *Audit) MarkHit(kind string, n int) {
	if a == nil {
		return
	}
	a.Hits += n
	if a.Kind == "" {
		a.Kind = kind
	}
}
