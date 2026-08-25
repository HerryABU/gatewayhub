package database

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gatewayhub/internal/config"
	"gatewayhub/internal/models"
)

// Init 初始化数据库连接并执行自动迁移
func Init(cfg *config.DatabaseConfig) (*gorm.DB, error) {
	// SQLite 数据库放独立目录（如 data/），自动创建目录避免启动失败
	if (cfg.Driver == "sqlite" || cfg.Driver == "") && cfg.DSN != "" && cfg.DSN != ":memory:" {
		if dir := filepath.Dir(cfg.DSN); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
	}
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite", "":
		dialector = sqlite.Open(cfg.DSN)
	case "mysql":
		dialector = mysql.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.AutoMigrate(
		&models.User{},
		&models.Route{},
		&models.AccessLog{},
		&models.IPGeoCache{},
		&models.DailyStat{},
		&models.Setting{},
		&models.IPRule{},
		&models.APIRule{},
		&models.BackupRecord{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	return db, nil
}

// SeedDemo 写入示例路由（建站向导完成后调用，幂等）。
// 注：不再生成演示访问日志——统计/地图页只展示真实流量，避免"测试数据"污染。
func SeedDemo(db *gorm.DB) error {
	return seedRoutes(db)
}

func seedRoutes(db *gorm.DB) error {
	var count int64
	db.Model(&models.Route{}).Count(&count)
	if count > 0 {
		return nil
	}
	routes := []models.Route{
		{Name: "Java-订单服务", Prefix: "java-order", Target: ":8080", Timeout: 5, Status: models.StatusActive},
		{Name: "Go-认证服务", Prefix: "go-auth", Target: ":8081/api/v1", Timeout: 3, Status: models.StatusActive},
		{Name: "Node-推送服务", Prefix: "node-push", Target: ":3000", Timeout: 5, Status: models.StatusActive},
		{Name: "Python-推荐服务", Prefix: "py-recommend", Target: ":5000", Timeout: 5, Status: models.StatusActive},
		{Name: "PHP-后台管理", Prefix: "php-admin", Target: ":8082/admin", Timeout: 8, Status: models.StatusInactive},
	}
	return db.Create(&routes).Error
}

