package models

import "time"

// User 用户表
type User struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Username  string    `gorm:"size:50;not null;uniqueIndex" json:"username"`
	Password  string    `gorm:"size:255;not null" json:"-"`
	Role      string    `gorm:"size:20;not null;default:admin" json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Route 路由配置表
type Route struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:100;not null" json:"name"`
	Prefix      string `gorm:"size:30;not null;uniqueIndex" json:"prefix"`
	Target      string `gorm:"size:500;not null" json:"target"`
	Description string `gorm:"size:500" json:"description"` // 站点介绍：该挂载站是做什么的（访客页展示）
	Timeout     int    `gorm:"not null;default:5" json:"timeout"`
	Interval    int    `gorm:"not null;default:30" json:"interval"`           // 健康检查间隔（秒），0 用全局默认
	Status      string `gorm:"size:20;not null;default:active" json:"status"` // active / inactive
	// CookieHat 非空时启用完整 cookie 隔离：请求侧脱帽 + 响应侧加帽 + Path 收紧。
	CookieHat   string `gorm:"size:64;default:''" json:"cookie_hat"`
	DropForeign bool   `gorm:"default:false" json:"drop_foreign_cookies"`
	// TLSSkipVerify 跳过后端 HTTPS 证书校验。默认关闭；宝塔面板 / 内网自签服务
	// 需显式打开。仅影响网关→后端这一段，不改变浏览器→网关的证书校验。
	TLSSkipVerify bool `gorm:"default:false" json:"tls_insecure_skip_verify"`
	// SDKOriginAuth 第三方登录 SDK（微信/Apple 等）的回调走原站域名：
	// shim 把 redirect_uri 从网关 origin 换成后端原站 origin，让开放平台的
	// 域名白名单校验通过（网关域名通常不在登记名单里）。登录流程在原站完成。
	SDKOriginAuth bool `gorm:"default:false" json:"sdk_origin_auth"`
	// SpoofHostname 分层欺骗：让前端环境判定以为跑在官方域名上（按 hostname 选配置）。
	SpoofHostname bool `gorm:"default:false" json:"spoof_hostname"`
	// WXAppid 非空时，shim 在 WxLogin 初始化时把 appid 换成该值（官方 appid），
	// 配合 SDKOriginAuth 让微信登录在反代域名下正常工作。
	WXAppid string `gorm:"size:64;default:''" json:"wx_appid"`
	// OAuthRedirectMap 通用回调域名修正：JSON 对象 {旧域名:新域名}。
	// shim 在第三方 OAuth URL 的 redirect_uri 里做域名替换，一次覆盖微信/QQ/飞书等。
	// 典型用途：把 staging/dev 回调域名（chat-dev.deepseek.com）换成生产域名。
	OAuthRedirectMap string    `gorm:"size:1000;default:''" json:"oauth_redirect_map"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// AccessLog 访问日志表
type AccessLog struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	RoutePrefix  string    `gorm:"size:30;not null;index:idx_logs_route_created" json:"route_prefix"`
	RequestPath  string    `gorm:"size:500" json:"request_path"`
	Method       string    `gorm:"size:10" json:"method"`
	StatusCode   int       `gorm:"index" json:"status_code"`
	ClientIP     string    `gorm:"size:45;index" json:"client_ip"`
	UserAgent    string    `gorm:"size:500" json:"user_agent"`
	ResponseTime int       `json:"response_time"`
	Country      string    `gorm:"size:64;index" json:"country"`
	CountryCode  string    `gorm:"size:8;index" json:"country_code"`
	Province     string    `gorm:"size:64;index" json:"province"`
	City         string    `gorm:"size:64;index" json:"city"`
	CreatedAt    time.Time `gorm:"index:idx_logs_route_created;index:idx_logs_created" json:"created_at"`
}

// Setting 系统设置（key-value）
type Setting struct {
	Key       string    `gorm:"size:64;primaryKey" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IPRule IP 黑名单/白名单规则
type IPRule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	IP        string    `gorm:"size:64;not null;uniqueIndex" json:"ip"`      // 支持 CIDR，如 1.2.3.0/24
	Action    string    `gorm:"size:10;not null;default:deny" json:"action"` // allow / deny
	Note      string    `gorm:"size:255" json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// APIRule API 路径黑名单/白名单规则
type APIRule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Path      string    `gorm:"size:255;not null;uniqueIndex" json:"path"`   // 前缀匹配，如 /api/admin
	Action    string    `gorm:"size:10;not null;default:deny" json:"action"` // allow / deny
	Note      string    `gorm:"size:255" json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupRecord 备份记录
type BackupRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Filename  string    `gorm:"size:255;not null" json:"filename"`
	Size      int64     `json:"size"`
	Kind      string    `gorm:"size:20" json:"kind"` // manual / scheduled
	CreatedAt time.Time `json:"created_at"`
}

// 安全动作常量
const (
	ActionAllow = "allow"
	ActionDeny  = "deny"
)

// IPGeoCache IP 地理位置缓存
type IPGeoCache struct {
	IP        string    `gorm:"size:45;primaryKey" json:"ip"`
	Country   string    `gorm:"size:64" json:"country"`
	Province  string    `gorm:"size:64" json:"province"`
	City      string    `gorm:"size:64" json:"city"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DailyStat 日统计汇总表（可选优化）
type DailyStat struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	RoutePrefix string    `gorm:"size:30;not null;uniqueIndex:idx_daily_route_date" json:"route_prefix"`
	StatDate    string    `gorm:"size:10;not null;uniqueIndex:idx_daily_route_date" json:"stat_date"`
	PV          int64     `gorm:"not null;default:0" json:"pv"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RouteStatus 路由状态常量
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

// HealthState 健康状态常量（绿/橙/红/灰）
const (
	HealthHealthy = "healthy" // 🟢 绿
	HealthWarning = "warning" // 🟠 橙
	HealthDown    = "down"    // 🔴 红
	HealthUnknown = "unknown" // ⚪ 灰
)
