package handlers

import (
	"github.com/gin-gonic/gin"

	"gatewayhub/internal/health"
	"gatewayhub/internal/models"
)

// HealthStatus GET /api/health —— 下级站点健康状态（绿/橙/红 + 分段历史）
func (h *Handler) HealthStatus(c *gin.Context) {
	var routes []models.Route
	if err := h.DB.Order("id asc").Find(&routes).Error; err != nil {
		h.fail(c, 2001, "查询失败")
		return
	}
	info := h.Health.InfoMap()
	history := h.Health.HistoryMap()
	list := make([]gin.H, 0, len(routes))
	for _, r := range routes {
		hi, ok := info[r.Prefix]
		if !ok {
			hi = health.Info{Status: models.HealthUnknown, LatencyMs: 0}
		}
		hist := history[r.Prefix]
		if hist == nil {
			hist = []health.HistoryPoint{}
		}
		list = append(list, gin.H{
			"name":       r.Name,
			"prefix":     r.Prefix,
			"target":     r.Target,
			"enabled":    r.Status == models.StatusActive,
			"status":     hi.Status,
			"latency_ms": hi.LatencyMs,
			"interval":   r.Interval,
			"history":    hist,
		})
	}
	h.ok(c, list)
}

// HealthCheckNow POST /api/health/check —— 立即触发一轮健康检查
func (h *Handler) HealthCheckNow(c *gin.Context) {
	h.Health.CheckAll()
	h.HealthStatus(c)
}
