package database

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gatewayhub/internal/config"
	"gatewayhub/internal/models"
)

// Init 初始化数据库连接并执行自动迁移
func Init(cfg *config.DatabaseConfig) (*gorm.DB, error) {
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

// SeedDemo 写入示例路由与演示访问数据（建站向导完成后调用，幂等）
func SeedDemo(db *gorm.DB) error {
	if err := seedRoutes(db); err != nil {
		return err
	}
	if err := seedAccessLogs(db); err != nil {
		return err
	}
	return nil
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

// demoGeoEntry 演示数据：IP → 地理位置的确定性映射
type demoGeoEntry struct {
	ip       string
	country  string
	province string
	city     string
	weight   int
}

var demoGeo = []demoGeoEntry{
	{"101.201.10.11", "中国", "北京市", "北京市", 30},
	{"101.80.120.33", "中国", "上海市", "上海市", 28},
	{"113.108.200.55", "中国", "广东省", "深圳市", 34},
	{"115.236.90.77", "中国", "浙江省", "杭州市", 22},
	{"180.101.130.88", "中国", "江苏省", "南京市", 20},
	{"118.112.60.99", "中国", "四川省", "成都市", 18},
	{"27.17.40.111", "中国", "湖北省", "武汉市", 16},
	{"113.128.80.123", "中国", "山东省", "济南市", 14},
	{"120.36.150.135", "中国", "福建省", "厦门市", 12},
	{"113.246.20.147", "中国", "湖南省", "长沙市", 11},
	{"124.115.30.159", "中国", "陕西省", "西安市", 10},
	{"123.184.70.171", "中国", "辽宁省", "沈阳市", 9},
	{"125.86.110.183", "中国", "重庆市", "重庆市", 8},
	{"111.30.160.195", "中国", "天津市", "天津市", 7},
	{"110.249.50.207", "中国", "河北省", "石家庄市", 6},
	{"123.52.90.219", "中国", "河南省", "郑州市", 6},
	{"112.26.120.231", "中国", "安徽省", "合肥市", 5},
	{"113.12.140.243", "中国", "广西壮族自治区", "南宁市", 5},
	{"116.52.170.255", "中国", "云南省", "昆明市", 4},
	{"60.13.180.33", "中国", "甘肃省", "兰州市", 3},
	{"150.109.30.66", "中国", "北京市", "北京市", 2},
	{"8.8.8.8", "美国", "", "", 2},
	{"103.11.99.101", "新加坡", "", "", 1},
}

var demoAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/125.0 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/126.0 Mobile Safari/537.36",
	"curl/8.5.0",
	"gatewayhub-client/1.0",
}

// countryCodes 演示数据国家 → ISO2 代码
var countryCodes = map[string]string{
	"中国":  "CN",
	"美国":  "US",
	"新加坡": "SG",
}

func seedAccessLogs(db *gorm.DB) error {
	var count int64
	db.Model(&models.AccessLog{}).Count(&count)
	if count > 0 {
		return nil
	}

	// 读取已配置的路由前缀
	var prefixes []string
	if err := db.Model(&models.Route{}).Where("status = ?", models.StatusActive).
		Pluck("prefix", &prefixes).Error; err != nil {
		return err
	}
	if len(prefixes) == 0 {
		prefixes = []string{"java-order", "go-auth", "node-push", "py-recommend"}
	}

	rng := rand.New(rand.NewSource(20260819))
	now := time.Now()
	totalWeight := 0
	for _, e := range demoGeo {
		totalWeight += e.weight
	}

	methods := []string{"GET", "GET", "GET", "POST", "GET", "PUT", "DELETE"}
	paths := map[string][]string{
		"java-order":   {"/java-order/order/list", "/java-order/order/detail", "/java-order/order/create", "/java-order/user/info"},
		"go-auth":      {"/go-auth/login", "/go-auth/refresh", "/go-auth/logout", "/go-auth/user/profile"},
		"node-push":    {"/node-push/send", "/node-push/topics", "/node-push/status"},
		"py-recommend": {"/py-recommend/feed", "/py-recommend/items", "/py-recommend/hot"},
	}

	const total = 2600
	logs := make([]models.AccessLog, 0, total)
	for i := 0; i < total; i++ {
		entry := weightedPick(rng, demoGeo, totalWeight)
		prefix := prefixes[rng.Intn(len(prefixes))]
		pathSet := paths[prefix]
		if len(pathSet) == 0 {
			pathSet = []string{"/" + prefix + "/index"}
		}
		method := methods[rng.Intn(len(methods))]

		// 状态码：95% 2xx，3% 4xx，2% 5xx
		status := 200
		r := rng.Intn(100)
		switch {
		case r < 95:
			status = 200 + rng.Intn(4) // 200-203
		case r < 98:
			status = 400 + rng.Intn(5) // 400-404
		default:
			status = 500 + rng.Intn(4) // 500-503
		}

		// 时间：近 7 天，日内有高峰（本地午夜对齐）
		dayOffset := rng.Intn(7)
		hour := int(rng.NormFloat64()*4 + 13) // 峰值在午后
		if hour < 0 {
			hour = 0
		}
		if hour > 23 {
			hour = 23
		}
		ts := localMidnight(now).AddDate(0, 0, -dayOffset).
			Add(time.Duration(hour)*time.Hour + time.Duration(rng.Intn(3600))*time.Second)

		logs = append(logs, models.AccessLog{
			RoutePrefix:  prefix,
			RequestPath:  pathSet[rng.Intn(len(pathSet))],
			Method:       method,
			StatusCode:   status,
			ClientIP:     entry.ip,
			UserAgent:    demoAgents[rng.Intn(len(demoAgents))],
			ResponseTime: 5 + rng.Intn(300),
			Country:      entry.country,
			CountryCode:  countryCodes[entry.country],
			Province:     entry.province,
			City:         entry.city,
			CreatedAt:    ts,
		})
	}

	return db.CreateInBatches(logs, 500).Error
}

func localMidnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func weightedPick(rng *rand.Rand, entries []demoGeoEntry, total int) demoGeoEntry {
	r := rng.Intn(total)
	cum := 0
	for _, e := range entries {
		cum += e.weight
		if r < cum {
			return e
		}
	}
	return entries[len(entries)-1]
}
