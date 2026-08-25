package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gatewayhub/internal/backup"
	"gatewayhub/internal/config"
	"gatewayhub/internal/geo"
	"gatewayhub/internal/health"
	"gatewayhub/internal/proxy"
	"gatewayhub/internal/security"
	"gatewayhub/internal/stats"
)

// Handler 聚合各依赖，供所有 handler 使用
type Handler struct {
	DB         *gorm.DB
	Cfg        *config.Config
	ConfigPath string
	Proxy      *proxy.Manager
	Health     *health.Checker
	Geo        *geo.Resolver
	Stats      *stats.Writer
	Security   *security.Manager
	Backup     *backup.Manager
}

func (h *Handler) ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": data})
}

func (h *Handler) okMsg(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": msg})
}

func (h *Handler) fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg})
}

func (h *Handler) failStatus(c *gin.Context, httpCode, code int, msg string) {
	c.JSON(httpCode, gin.H{"code": code, "message": msg})
}
