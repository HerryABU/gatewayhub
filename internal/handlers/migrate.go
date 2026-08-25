package handlers

import (
	"log"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/migrate"
)

// MigrateInfo GET /api/migrate/info
func (h *Handler) MigrateInfo(c *gin.Context) {
	h.ok(c, gin.H{
		"driver": h.Cfg.Database.Driver,
		"dsn":    h.Cfg.Database.DSN,
	})
}

// MigrateTest POST /api/migrate/test
func (h *Handler) MigrateTest(c *gin.Context) {
	var t migrate.Target
	if err := c.ShouldBindJSON(&t); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if err := migrate.TestConnection(t); err != nil {
		h.fail(c, 2002, "连接失败："+err.Error())
		return
	}
	h.okMsg(c, "connection ok")
}

// MigrateRun POST /api/migrate/run
func (h *Handler) MigrateRun(c *gin.Context) {
	var t migrate.Target
	if err := c.ShouldBindJSON(&t); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if t.Driver == h.Cfg.Database.Driver {
		// 同类型迁移（如 MySQL → MySQL），也走复制
	}
	summary, err := migrate.Migrate(h.DB, t)
	if err != nil {
		h.fail(c, 2001, "迁移失败："+err.Error())
		return
	}
	dsn, _ := t.DSN()
	h.Cfg.Database.Driver = t.Driver
	h.Cfg.Database.DSN = dsn
	configSaved := true
	if err := h.Cfg.Save(h.ConfigPath); err != nil {
		log.Printf("警告：保存配置失败：%v", err)
		configSaved = false
	}
	h.ok(c, gin.H{
		"summary":      summary,
		"restart":      true,
		"config_saved": configSaved,
		"note":         "迁移完成，配置已更新，请重启服务以切换到新数据库",
	})
}
