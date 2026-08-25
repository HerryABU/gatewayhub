package migrate

import (
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	glebarez "github.com/glebarez/sqlite"

	"gatewayhub/internal/models"
)

// Target 目标数据库配置
type Target struct {
	Driver   string `json:"driver"` // sqlite / mysql
	Path     string `json:"path"`   // sqlite 文件路径
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

// DSN 构造连接串
func (t Target) DSN() (string, error) {
	switch t.Driver {
	case "sqlite":
		if t.Path == "" {
			return "", fmt.Errorf("sqlite path required")
		}
		return t.Path, nil
	case "mysql":
		if t.Host == "" || t.Port == 0 || t.Database == "" {
			return "", fmt.Errorf("mysql host/port/database required")
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			t.User, t.Password, t.Host, t.Port, t.Database), nil
	default:
		return "", fmt.Errorf("unsupported driver: %s", t.Driver)
	}
}

// Open 打开数据库连接
func Open(t Target) (*gorm.DB, error) {
	dsn, err := t.DSN()
	if err != nil {
		return nil, err
	}
	switch t.Driver {
	case "sqlite":
		return gorm.Open(glebarez.Open(dsn), &gorm.Config{})
	case "mysql":
		return gorm.Open(mysql.Open(dsn), &gorm.Config{})
	default:
		return nil, fmt.Errorf("unsupported driver: %s", t.Driver)
	}
}

// TestConnection 测试目标数据库连接
func TestConnection(t Target) error {
	db, err := Open(t)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	return sqlDB.Ping()
}

// copyTable 泛型复制单表数据（目标先清空，源数据为准）
func copyTable[T any](src, dst *gorm.DB, batchSize int) error {
	if err := dst.AutoMigrate(new(T)); err != nil {
		return err
	}
	if err := dst.Where("1 = 1").Delete(new(T)).Error; err != nil {
		return err
	}
	var rows []T
	result := src.FindInBatches(&rows, batchSize, func(tx *gorm.DB, batch int) error {
		return dst.Create(rows).Error
	})
	return result.Error
}

// Migrate 将源数据库数据迁移到目标数据库
func Migrate(src *gorm.DB, t Target) (map[string]int64, error) {
	dst, err := Open(t)
	if err != nil {
		return nil, err
	}
	sqlDB, err := dst.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()

	summary := make(map[string]int64)
	count := func(dst *gorm.DB, name string, model interface{}) {
		var c int64
		dst.Model(model).Count(&c)
		summary[name] = c
	}

	type migrator struct {
		name  string
		run   func() error
		model interface{}
	}

	steps := []migrator{
		{"users", func() error { return copyTable[models.User](src, dst, 500) }, &models.User{}},
		{"routes", func() error { return copyTable[models.Route](src, dst, 500) }, &models.Route{}},
		{"settings", func() error { return copyTable[models.Setting](src, dst, 500) }, &models.Setting{}},
		{"ip_rules", func() error { return copyTable[models.IPRule](src, dst, 500) }, &models.IPRule{}},
		{"api_rules", func() error { return copyTable[models.APIRule](src, dst, 500) }, &models.APIRule{}},
		{"backup_records", func() error { return copyTable[models.BackupRecord](src, dst, 500) }, &models.BackupRecord{}},
		{"ip_geo_caches", func() error { return copyTable[models.IPGeoCache](src, dst, 500) }, &models.IPGeoCache{}},
		{"daily_stats", func() error { return copyTable[models.DailyStat](src, dst, 500) }, &models.DailyStat{}},
		{"access_logs", func() error { return copyTable[models.AccessLog](src, dst, 2000) }, &models.AccessLog{}},
	}

	for _, s := range steps {
		if err := s.run(); err != nil {
			return nil, fmt.Errorf("migrate %s: %w", s.name, err)
		}
		count(dst, s.name, s.model)
	}
	return summary, nil
}
