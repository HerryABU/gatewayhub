package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	ReadTimeout  int    `yaml:"read_timeout"`
	WriteTimeout int    `yaml:"write_timeout"`
	BaseDomain   string `yaml:"base_domain"` // 子域名后缀，如 localhost / myapp.local；空则禁用子域名路由
}

type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type JWTConfig struct {
	Secret       string `yaml:"secret"`
	Expires      string `yaml:"expires"`
	RememberDays int    `yaml:"remember_days"`
}

type HealthConfig struct {
	Interval         int    `yaml:"interval"`
	Timeout          int    `yaml:"timeout"`
	FailThreshold    int    `yaml:"fail_threshold"`
	RecoverThreshold int    `yaml:"recover_threshold"`
	HealthEndpoint   string `yaml:"health_endpoint"`
	SlowThreshold    int    `yaml:"slow_threshold"` // 超过该毫秒数判定为"缓慢"(橙)，默认 1000
}

type StatsConfig struct {
	RetainDays    int     `yaml:"retain_days"`
	BatchSize     int     `yaml:"batch_size"`
	BatchInterval int     `yaml:"batch_interval"`
	SampleRate    float64 `yaml:"sample_rate"`
}

type GeoConfig struct {
	Enabled        bool   `yaml:"enabled"`
	DBPath         string `yaml:"db_path"`
	OnlineFallback string `yaml:"online_fallback"`
}

type LogConfig struct {
	Level    string `yaml:"level"`
	Output   string `yaml:"output"`
	FilePath string `yaml:"file_path"`
}

type AccessLogConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否落盘访问日志（含请求头）
	Dir     string `yaml:"dir"`     // 日志根目录，按 天/小时 组织：{dir}/YYYY-MM-DD/HH.log
}

type SecurityConfig struct {
	Enabled       bool    `yaml:"enabled"`
	RateLimit     int     `yaml:"rate_limit"`      // 每 IP 每秒最大请求数
	Burst         int     `yaml:"burst"`           // 突发峰值
	BanThreshold  int     `yaml:"ban_threshold"`   // 触发封禁的连续超限次数
	BanDuration   int     `yaml:"ban_duration"`    // 自动封禁时长（秒）
	WAFEnabled    bool    `yaml:"waf_enabled"`     // SQL/XSS 拦截
	WAFBodyMax    int     `yaml:"waf_body_max"`    // WAF 请求体扫描上限（字节），超限跳过扫描（默认 512KB）
	GlobalRPS     int     `yaml:"global_rps"`      // 全局每秒最大请求数（0=不限）
	TrustProxies  bool    `yaml:"trust_proxies"`   // 是否信任 X-Forwarded-For
}

type BackupConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Dir       string `yaml:"dir"`       // 备份目录
	Interval  string `yaml:"interval"`  // 备份间隔，如 "24h"
	Retain    int    `yaml:"retain"`    // 保留最近 N 份
}

type SetupConfig struct {
	AllowRemote bool `yaml:"allow_remote"` // 是否允许远程访问建站向导（默认仅 localhost）
}

type Config struct {
	Server      ServerConfig    `yaml:"server"`
	Database    DatabaseConfig  `yaml:"database"`
	JWT         JWTConfig       `yaml:"jwt"`
	HealthCheck HealthConfig    `yaml:"health_check"`
	Stats       StatsConfig     `yaml:"stats"`
	Geo         GeoConfig       `yaml:"geo"`
	Log         LogConfig       `yaml:"log"`
	AccessLog   AccessLogConfig `yaml:"access_log"`
	Security    SecurityConfig  `yaml:"security"`
	Backup      BackupConfig    `yaml:"backup"`
	Setup       SetupConfig     `yaml:"setup"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8088,
			ReadTimeout:  30,
			WriteTimeout: 30,
			BaseDomain:   "localhost",
		},
		Database: DatabaseConfig{Driver: "sqlite", DSN: "data/gateway.db"},
		JWT: JWTConfig{
			Secret:       "gatewayhub-change-me-in-production",
			Expires:      "24h",
			RememberDays: 7,
		},
		HealthCheck: HealthConfig{
			Interval:         30,
			Timeout:          2,
			FailThreshold:    3,
			RecoverThreshold: 2,
			HealthEndpoint:   "/health",
			SlowThreshold:    1000,
		},
		Stats: StatsConfig{
			RetainDays:    180,
			BatchSize:     100,
			BatchInterval: 1,
			SampleRate:    1.0,
		},
		Geo: GeoConfig{
			Enabled: true,
			DBPath:  "data/ip2region.xdb",
		},
		Log: LogConfig{Level: "info", Output: "stdout"},
		AccessLog: AccessLogConfig{
			Enabled: true,
			Dir:     "logs",
		},
		Security: SecurityConfig{
			Enabled:      true,
			RateLimit:    100,
			Burst:        200,
			BanThreshold: 5,
			BanDuration:  300,
			WAFEnabled:   true,
			WAFBodyMax:   512 * 1024,
			GlobalRPS:    0,
			TrustProxies: true,
		},
		Backup: BackupConfig{
			Enabled:  true,
			Dir:      "backups",
			Interval: "24h",
			Retain:   30,
		},
		Setup: SetupConfig{AllowRemote: false},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = "config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // 使用默认配置
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func (c *Config) JWTExpires() time.Duration {
	d, err := time.ParseDuration(c.JWT.Expires)
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return d
}

func (c *Config) RememberExpires() time.Duration {
	days := c.JWT.RememberDays
	if days <= 0 {
		days = 7
	}
	return time.Duration(days) * 24 * time.Hour
}

// Save 将配置写回文件
func (c *Config) Save(path string) error {
	if path == "" {
		path = "config.yaml"
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
