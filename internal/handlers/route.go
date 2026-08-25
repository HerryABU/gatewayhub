package handlers

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gatewayhub/internal/health"
	"gatewayhub/internal/models"
	"gatewayhub/internal/target"
)

var prefixRe = regexp.MustCompile(`^[a-z][a-z0-9\-]*(\/[a-z][a-z0-9\-]*)*$`)

var reservedPrefixes = map[string]bool{
	"api":     true,
	"assets":  true,
	"static":  true,
	"favicon": true,
}

type routeReq struct {
	Name     string `json:"name"`
	Target   string `json:"target"`
	Timeout  int    `json:"timeout"`
	Interval int    `json:"interval"`
	Status   string `json:"status"`
}

// validatePrefix 校验转发名（支持多级前缀，如 "v2/beta"，每段以小写字母开头）
func validatePrefix(prefix string) (string, bool) {
	prefix = strings.TrimSpace(prefix)
	if len(prefix) < 3 || len(prefix) > 60 {
		return "", false
	}
	if !prefixRe.MatchString(prefix) {
		return "", false
	}
	first := prefix
	if i := strings.Index(prefix, "/"); i >= 0 {
		first = prefix[:i]
	}
	if reservedPrefixes[first] {
		return "", false
	}
	return prefix, true
}

// ListRoutes GET /api/routes（公开，合并健康状态 + 延迟 + 分段历史）
func (h *Handler) ListRoutes(c *gin.Context) {
	var routes []models.Route
	if err := h.DB.Order("id asc").Find(&routes).Error; err != nil {
		h.fail(c, 2001, "查询失败")
		return
	}
	infoMap := h.Health.InfoMap()
	historyMap := h.Health.HistoryMap()
	type item struct {
		models.Route
		Health    string               `json:"health"`
		LatencyMs int64                `json:"latency_ms"`
		History   []health.HistoryPoint `json:"history"`
	}
	list := make([]item, 0, len(routes))
	for _, r := range routes {
		info, ok := infoMap[r.Prefix]
		if !ok {
			info = health.Info{Status: models.HealthUnknown}
		}
		hist := historyMap[r.Prefix]
		if hist == nil {
			hist = []health.HistoryPoint{}
		}
		list = append(list, item{
			Route:     r,
			Health:    info.Status,
			LatencyMs: info.LatencyMs,
			History:   hist,
		})
	}
	h.ok(c, list)
}

// CreateRoute POST /api/routes
func (h *Handler) CreateRoute(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required"`
		Prefix   string `json:"prefix" binding:"required"`
		Target   string `json:"target" binding:"required"`
		Timeout  int    `json:"timeout"`
		Interval int    `json:"interval"`
		Status   string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 2 || len(name) > 30 {
		h.fail(c, 1001, "服务名称长度须为 2-30 字符")
		return
	}
	prefix, ok := validatePrefix(req.Prefix)
	if !ok {
		h.fail(c, 1001, "转发名须为 3-60 字符，以小写字母开头，每段仅含小写字母/数字/连字符（可用 / 分隔多级，如 v2/beta），且不可为保留名")
		return
	}
	if _, err := target.Parse(req.Target); err != nil {
		h.fail(c, 1001, "后端地址格式错误，支持 :8080、:8080/api/v1 或完整 URL")
		return
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 5
	}
	if timeout < 1 || timeout > 60 {
		h.fail(c, 1001, "超时时间须在 1-60 秒之间")
		return
	}
	interval := req.Interval
	if interval == 0 {
		interval = 30
	}
	if interval < 5 || interval > 86400 {
		h.fail(c, 1001, "检查间隔须在 5-86400 秒之间")
		return
	}
	status := req.Status
	if status == "" {
		status = models.StatusActive
	}
	if status != models.StatusActive && status != models.StatusInactive {
		h.fail(c, 1001, "状态非法")
		return
	}

	var count int64
	h.DB.Model(&models.Route{}).Where("prefix = ?", prefix).Count(&count)
	if count > 0 {
		h.fail(c, 1003, "转发名 '"+prefix+"' 已存在")
		return
	}

	route := models.Route{
		Name:     name,
		Prefix:   prefix,
		Target:   strings.TrimSpace(req.Target),
		Timeout:  timeout,
		Interval: interval,
		Status:   status,
	}
	if err := h.DB.Create(&route).Error; err != nil {
		h.fail(c, 2001, "创建失败")
		return
	}
	// 热加载到内存路由表
	if err := h.Proxy.Upsert(route); err != nil {
		h.fail(c, 2001, "路由热加载失败")
		return
	}
	h.ok(c, route)
}

// UpdateRoute PUT /api/routes/:prefix（转发名不可修改）
func (h *Handler) UpdateRoute(c *gin.Context) {
	prefix := c.Param("prefix")
	var route models.Route
	if err := h.DB.Where("prefix = ?", prefix).First(&route).Error; err != nil {
		h.fail(c, 1002, "路由不存在")
		return
	}
	var req routeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 2 || len(name) > 30 {
		h.fail(c, 1001, "服务名称长度须为 2-30 字符")
		return
	}
	if _, err := target.Parse(req.Target); err != nil {
		h.fail(c, 1001, "后端地址格式错误")
		return
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = route.Timeout
	}
	if timeout < 1 || timeout > 60 {
		h.fail(c, 1001, "超时时间须在 1-60 秒之间")
		return
	}
	interval := req.Interval
	if interval == 0 {
		interval = route.Interval
	}
	if interval < 5 || interval > 86400 {
		h.fail(c, 1001, "检查间隔须在 5-86400 秒之间")
		return
	}
	status := req.Status
	if status == "" {
		status = route.Status
	}
	if status != models.StatusActive && status != models.StatusInactive {
		h.fail(c, 1001, "状态非法")
		return
	}

	route.Name = name
	route.Target = strings.TrimSpace(req.Target)
	route.Timeout = timeout
	route.Interval = interval
	route.Status = status
	if err := h.DB.Save(&route).Error; err != nil {
		h.fail(c, 2001, "更新失败")
		return
	}
	if err := h.Proxy.Upsert(route); err != nil {
		h.fail(c, 2001, "路由热加载失败")
		return
	}
	h.okMsg(c, "route updated")
}

// DeleteRoute DELETE /api/routes/:prefix
func (h *Handler) DeleteRoute(c *gin.Context) {
	prefix := c.Param("prefix")
	var route models.Route
	if err := h.DB.Where("prefix = ?", prefix).First(&route).Error; err != nil {
		h.fail(c, 1002, "路由不存在")
		return
	}
	if err := h.DB.Delete(&route).Error; err != nil {
		h.fail(c, 2001, "删除失败")
		return
	}
	h.Proxy.Remove(prefix)
	h.okMsg(c, "route deleted")
}

// UpdateRouteStatus PATCH /api/routes/:prefix/status
func (h *Handler) UpdateRouteStatus(c *gin.Context) {
	prefix := c.Param("prefix")
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if req.Status != models.StatusActive && req.Status != models.StatusInactive {
		h.fail(c, 1001, "状态非法")
		return
	}
	var route models.Route
	if err := h.DB.Where("prefix = ?", prefix).First(&route).Error; err != nil {
		h.fail(c, 1002, "路由不存在")
		return
	}
	route.Status = req.Status
	if err := h.DB.Save(&route).Error; err != nil {
		h.fail(c, 2001, "更新失败")
		return
	}
	if err := h.Proxy.Upsert(route); err != nil {
		h.fail(c, 2001, "路由热加载失败")
		return
	}
	h.okMsg(c, "status updated")
}

var _ = gorm.ErrRecordNotFound
