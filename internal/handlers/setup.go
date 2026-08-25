package handlers

import (
	"log"
	"strings"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/auth"
	"gatewayhub/internal/database"
	"gatewayhub/internal/migrate"
	"gatewayhub/internal/models"
)

// SetupStatus GET /api/setup/status
func (h *Handler) SetupStatus(c *gin.Context) {
	h.ok(c, gin.H{
		"configured":      h.IsConfigured(),
		"site_name":       h.getSetting("site_name"),
		"language":        h.getSetting("language"),
		"current_driver":  h.Cfg.Database.Driver,
		"current_dsn":     h.Cfg.Database.DSN,
	})
}

// SetupConfigure POST /api/setup/configure —— 一次性完成建站
func (h *Handler) SetupConfigure(c *gin.Context) {
	if h.IsConfigured() {
		h.failStatus(c, 404, 1002, "setup already completed")
		return
	}
	var req struct {
		SiteName      string         `json:"site_name"`
		Language      string         `json:"language"`
		AdminUsername string         `json:"admin_username"`
		AdminPassword string         `json:"admin_password"`
		AdminEmail    string         `json:"admin_email"`
		DB            migrate.Target `json:"db"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	req.SiteName = strings.TrimSpace(req.SiteName)
	req.AdminUsername = strings.TrimSpace(req.AdminUsername)
	if req.SiteName == "" {
		h.fail(c, 1001, "站点名称不能为空")
		return
	}
	if len(req.AdminUsername) < 3 {
		h.fail(c, 1001, "管理员用户名至少 3 位")
		return
	}
	if len(req.AdminPassword) < 6 {
		h.fail(c, 1001, "管理员密码至少 6 位")
		return
	}
	if req.Language == "" {
		req.Language = "zh-CN"
	}

	// 1. 创建管理员账号（若不存在）
	var count int64
	h.DB.Model(&models.User{}).Where("username = ?", req.AdminUsername).Count(&count)
	if count == 0 {
		hash, err := auth.HashPassword(req.AdminPassword)
		if err != nil {
			h.fail(c, 2001, "密码加密失败")
			return
		}
		user := models.User{Username: req.AdminUsername, Password: hash, Role: "admin"}
		if err := h.DB.Create(&user).Error; err != nil {
			h.fail(c, 2001, "创建管理员失败")
			return
		}
	}

	// 2. 保存站点设置
	_ = h.setSetting("site_name", req.SiteName)
	_ = h.setSetting("language", req.Language)
	_ = h.setSetting("setup_completed", "true")

	// 2.1 写入演示路由与访问数据（便于体验，幂等）
	if err := database.SeedDemo(h.DB); err != nil {
		log.Printf("演示数据初始化失败（可忽略）: %v", err)
	}
	_ = h.Proxy.Load()

	// 3. 若指定了不同数据库，执行迁移并更新配置
	switched := false
	if req.DB.Driver != "" && req.DB.Driver != h.Cfg.Database.Driver {
		if _, err := migrate.Migrate(h.DB, req.DB); err != nil {
			_ = h.setSetting("setup_completed", "false") // 回滚标记
			h.fail(c, 2001, "数据库迁移失败："+err.Error())
			return
		}
		dsn, _ := req.DB.DSN()
		h.Cfg.Database.Driver = req.DB.Driver
		h.Cfg.Database.DSN = dsn
		switched = true
	}

	// 4. 持久化配置（失败仅告警，不影响建站完成）
	configSaved := true
	if err := h.Cfg.Save(h.ConfigPath); err != nil {
		log.Printf("警告：保存配置到 %s 失败：%v", h.ConfigPath, err)
		configSaved = false
	}

	h.ok(c, gin.H{
		"configured":   true,
		"site_name":    req.SiteName,
		"db_switched":  switched,
		"restart":      switched,
		"config_saved": configSaved,
	})
}
