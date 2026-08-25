package handlers

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/models"
)

// ---------- IP 黑白名单 ----------

// ListIPRules GET /api/security/ips
func (h *Handler) ListIPRules(c *gin.Context) {
	var rules []models.IPRule
	if err := h.DB.Order("id desc").Find(&rules).Error; err != nil {
		h.fail(c, 2001, "查询失败")
		return
	}
	h.ok(c, rules)
}

// CreateIPRule POST /api/security/ips
func (h *Handler) CreateIPRule(c *gin.Context) {
	var req struct {
		IP     string `json:"ip" binding:"required"`
		Action string `json:"action" binding:"required"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if !validIPOrCIDR(req.IP) {
		h.fail(c, 1001, "IP 格式错误，支持单 IP 或 CIDR（如 1.2.3.0/24）")
		return
	}
	if req.Action != models.ActionAllow && req.Action != models.ActionDeny {
		h.fail(c, 1001, "动作非法")
		return
	}
	rule := models.IPRule{IP: req.IP, Action: req.Action, Note: req.Note}
	if err := h.DB.Create(&rule).Error; err != nil {
		h.fail(c, 2001, "创建失败（可能已存在）")
		return
	}
	h.Security.Refresh()
	h.ok(c, rule)
}

// DeleteIPRule DELETE /api/security/ips/:id
func (h *Handler) DeleteIPRule(c *gin.Context) {
	id := c.Param("id")
	if err := h.DB.Delete(&models.IPRule{}, id).Error; err != nil {
		h.fail(c, 2001, "删除失败")
		return
	}
	h.Security.Refresh()
	h.okMsg(c, "deleted")
}

// ---------- API 路径黑白名单 ----------

// ListAPIRules GET /api/security/apis
func (h *Handler) ListAPIRules(c *gin.Context) {
	var rules []models.APIRule
	if err := h.DB.Order("id desc").Find(&rules).Error; err != nil {
		h.fail(c, 2001, "查询失败")
		return
	}
	h.ok(c, rules)
}

// CreateAPIRule POST /api/security/apis
func (h *Handler) CreateAPIRule(c *gin.Context) {
	var req struct {
		Path   string `json:"path" binding:"required"`
		Action string `json:"action" binding:"required"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" || !strings.HasPrefix(req.Path, "/") {
		h.fail(c, 1001, "路径须以 / 开头")
		return
	}
	if req.Action != models.ActionAllow && req.Action != models.ActionDeny {
		h.fail(c, 1001, "动作非法")
		return
	}
	rule := models.APIRule{Path: req.Path, Action: req.Action, Note: req.Note}
	if err := h.DB.Create(&rule).Error; err != nil {
		h.fail(c, 2001, "创建失败（可能已存在）")
		return
	}
	h.Security.Refresh()
	h.ok(c, rule)
}

// DeleteAPIRule DELETE /api/security/apis/:id
func (h *Handler) DeleteAPIRule(c *gin.Context) {
	id := c.Param("id")
	if err := h.DB.Delete(&models.APIRule{}, id).Error; err != nil {
		h.fail(c, 2001, "删除失败")
		return
	}
	h.Security.Refresh()
	h.okMsg(c, "deleted")
}

// validIPOrCIDR 校验 IP 或 CIDR
func validIPOrCIDR(s string) bool {
	if strings.Contains(s, "/") {
		_, _, err := net.ParseCIDR(s)
		return err == nil
	}
	return net.ParseIP(s) != nil
}
