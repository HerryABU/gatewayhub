// Package isolation 提供路径隔离网关的管理接口（配置/状态/指标/降级/热加载）。
// 独立成包而非继续堆进 handlers：它只依赖隔离引擎，与网关控制台其余职责解耦。
package isolation

import (
	"net/http"

	"github.com/gin-gonic/gin"

	isoengine "gatewayhub/internal/isolation/engine"
)

// Handler 是隔离网关管理接口处理器。
type Handler struct {
	Engine *isoengine.Engine
	// File 是服务配置表路径（重新加载时使用）。
	File string
}

// New 创建处理器；engine 为 nil 表示功能未启用（接口返回提示而不是 500）。
func New(e *isoengine.Engine, file string) *Handler {
	return &Handler{Engine: e, File: file}
}

// List 返回按服务聚合的「配置 + 运行状态」（FR-19）。
//
// GET /api/isolation/services
func (h *Handler) List(c *gin.Context) {
	if h.Engine == nil {
		h.ok(c, gin.H{"enabled": false, "services": []any{}, "message": "isolation gateway disabled"})
		return
	}
	tbl := h.Engine.Table()
	warnings := h.Engine.Warnings()
	if warnings == nil {
		warnings = []string{} // 前端按数组处理，避免 null 触发渲染异常
	}
	h.ok(c, gin.H{
		"enabled":    true,
		"source":     h.File,
		"hash":       tbl.Hash,
		"loaded_at":  tbl.LoadedAt,
		"count":      h.Engine.Count(),
		"warnings":   warnings,
		"services":   h.Engine.Snapshot(),
		"audit_file": h.Engine.AuditPath(),
	})
}

// Metrics 只返回指标（便于接监控抓取）。
//
// GET /api/isolation/metrics
func (h *Handler) Metrics(c *gin.Context) {
	if h.Engine == nil {
		h.fail(c, 1001, "isolation gateway disabled")
		return
	}
	h.ok(c, h.Engine.Metrics())
}

// ok 输出统一成功结构。
func (h *Handler) ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": data})
}

// fail 输出统一失败结构（HTTP 200 + 业务码，与网关其他接口一致）。
func (h *Handler) fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg})
}
