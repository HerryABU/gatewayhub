package isolation

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/isolation/config"
)

// SetMode 一键切换某服务的处理模式（FR-14：降级为 isolated 只需一次调用）。
//
// PATCH /api/isolation/services/:name/mode   body: {"mode":"isolated"}
func (h *Handler) SetMode(c *gin.Context) {
	if h.Engine == nil {
		h.fail(c, 1001, "isolation gateway disabled")
		return
	}
	name := c.Param("name")
	var body struct {
		Mode string `json:"mode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.fail(c, 1002, "缺少 mode 参数")
		return
	}
	mode, ok := config.ParseMode(body.Mode)
	if !ok {
		h.fail(c, 1003, "未知模式（可选 pass-through / strip / isolated）")
		return
	}
	if err := h.Engine.SetMode(name, mode, actorOf(c)); err != nil {
		h.fail(c, 1004, err.Error())
		return
	}
	h.ok(c, gin.H{"service": name, "mode": mode.Canonical()})
}

// Reload 从配置文件重新加载服务表（失败时保留原配置）。
//
// POST /api/isolation/reload
func (h *Handler) Reload(c *gin.Context) {
	if h.Engine == nil {
		h.fail(c, 1001, "isolation gateway disabled")
		return
	}
	tbl, err := h.Engine.ReloadFromFile(h.File, actorOf(c))
	if err != nil {
		h.fail(c, 1005, err.Error())
		return
	}
	h.ok(c, gin.H{
		"hash":     tbl.Hash,
		"count":    len(tbl.Services),
		"warnings": tbl.Warnings,
	})
}

// Audit 返回最近的配置变更审计记录（FR-4）。
//
// GET /api/isolation/audit?limit=50
func (h *Handler) Audit(c *gin.Context) {
	if h.Engine == nil {
		h.fail(c, 1001, "isolation gateway disabled")
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	h.ok(c, gin.H{
		"file":    h.Engine.AuditPath(),
		"entries": h.Engine.AuditRecent(limit),
	})
}

// actorOf 取操作者（JWT 中间件写入），未登录时记为 anonymous。
func actorOf(c *gin.Context) string {
	if u := c.GetString("username"); u != "" {
		return u
	}
	return "anonymous"
}
