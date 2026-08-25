package handlers

import (
	"github.com/gin-gonic/gin"

	"gatewayhub/internal/models"
)

// getSetting 读取系统设置
func (h *Handler) getSetting(key string) string {
	var s models.Setting
	if err := h.DB.Where("key = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

// setSetting 写入系统设置
func (h *Handler) setSetting(key, value string) error {
	var s models.Setting
	if err := h.DB.Where("key = ?", key).First(&s).Error; err == nil {
		return h.DB.Model(&s).Update("value", value).Error
	}
	return h.DB.Create(&models.Setting{Key: key, Value: value}).Error
}

// IsConfigured 是否已完成建站向导
func (h *Handler) IsConfigured() bool {
	return h.getSetting("setup_completed") == "true"
}

// SiteName 获取站点名称
func (h *Handler) SiteName() string {
	if n := h.getSetting("site_name"); n != "" {
		return n
	}
	return "GatewayHub"
}

// GetSettings GET /api/settings（公开只读站点信息）
func (h *Handler) GetSettings(c *gin.Context) {
	h.ok(c, gin.H{
		"site_name":  h.getSetting("site_name"),
		"site_intro": h.getSetting("site_intro"),
		"language":   h.getSetting("language"),
		"configured": h.IsConfigured(),
		"db_driver":  h.Cfg.Database.Driver,
		"version":    "1.1.0",
	})
}

// UpdateSettings PUT /api/settings（管理员）
func (h *Handler) UpdateSettings(c *gin.Context) {
	var req struct {
		SiteName  string  `json:"site_name"`
		SiteIntro *string `json:"site_intro"`
		Language  string  `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if req.SiteName != "" {
		_ = h.setSetting("site_name", req.SiteName)
	}
	// 站点介绍支持多行文本；用指针区分「未传」与「传空串清空」
	if req.SiteIntro != nil {
		_ = h.setSetting("site_intro", *req.SiteIntro)
	}
	if req.Language != "" {
		_ = h.setSetting("language", req.Language)
	}
	_ = h.Cfg.Save(h.ConfigPath)
	h.okMsg(c, "settings updated")
}
