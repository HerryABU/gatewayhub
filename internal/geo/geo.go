package geo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"

	"gatewayhub/internal/config"
)

// Result IP 地理位置解析结果
type Result struct {
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	Province    string `json:"province"`
	City        string `json:"city"`
}

// Resolver IP 地理位置解析器（离线 ip2region 为主，在线兜底）
type Resolver struct {
	mu       sync.RWMutex
	searcher *xdb.Searcher
	cache    sync.Map // ip -> Result
	cfg      config.GeoConfig
	client   *http.Client
	loaded   bool
}

// New 创建解析器，加载离线 IP 库
func New(cfg config.GeoConfig) *Resolver {
	r := &Resolver{
		cfg:    cfg,
		client: &http.Client{Timeout: 3 * time.Second},
	}
	if !cfg.Enabled {
		return r
	}
	if cfg.DBPath != "" {
		if err := r.loadDB(cfg.DBPath); err != nil {
			r.loaded = false
		} else {
			r.loaded = true
		}
	}
	return r
}

func (r *Resolver) loadDB(path string) error {
	buff, err := xdb.LoadContentFromFile(path)
	if err != nil {
		return err
	}
	searcher, err := xdb.NewWithBuffer(xdb.IPv4, buff)
	if err != nil {
		return err
	}
	r.searcher = searcher
	return nil
}

// Loaded 返回离线库是否加载成功
func (r *Resolver) Loaded() bool {
	return r.loaded
}

// Resolve 解析 IP 地址到地理位置，带内存缓存
func (r *Resolver) Resolve(ip string) Result {
	if ip == "" {
		return Result{}
	}
	if v, ok := r.cache.Load(ip); ok {
		return v.(Result)
	}

	var res Result
	// 内网地址直接返回空
	if isPrivateIP(ip) {
		res = Result{}
	} else if r.loaded {
		res = r.resolveOffline(ip)
	}

	if res.Country == "" && r.cfg.OnlineFallback != "" {
		res = r.resolveOnline(ip)
	}

	r.cache.Store(ip, res)
	return res
}

func (r *Resolver) resolveOffline(ip string) Result {
	// Searcher 非线程安全，需加锁串行查询
	r.mu.Lock()
	defer r.mu.Unlock()
	searcher := r.searcher
	if searcher == nil {
		return Result{}
	}
	region, err := searcher.Search(ip)
	if err != nil || region == "" {
		return Result{}
	}
	// ip2region v4 格式：国家|省份|城市|ISP|国家代码
	parts := strings.Split(region, "|")
	res := Result{}
	if len(parts) > 0 {
		res.Country = normalizeCountry(parts[0])
	}
	if len(parts) > 1 {
		res.Province = normalize(parts[1])
	}
	if len(parts) > 2 {
		res.City = normalize(parts[2])
	}
	if len(parts) > 4 {
		res.CountryCode = strings.ToUpper(strings.TrimSpace(parts[4]))
	}
	return res
}

// countryAlias 将 ip2region 返回的英文国家名归一化为中文（与演示数据保持一致）
var countryAlias = map[string]string{
	"China":                  "中国",
	"United States":          "美国",
	"United States of America": "美国",
	"Singapore":              "新加坡",
	"Japan":                  "日本",
	"South Korea":            "韩国",
	"Korea":                  "韩国",
	"Republic of Korea":      "韩国",
	"United Kingdom":         "英国",
	"Great Britain":          "英国",
	"Germany":                "德国",
	"France":                 "法国",
	"Russia":                 "俄罗斯",
	"Russian Federation":     "俄罗斯",
	"Canada":                 "加拿大",
	"Australia":              "澳大利亚",
	"India":                  "印度",
	"Malaysia":               "马来西亚",
	"Thailand":               "泰国",
	"Vietnam":                "越南",
	"Indonesia":              "印度尼西亚",
	"Philippines":            "菲律宾",
	"Netherlands":            "荷兰",
	"Italy":                  "意大利",
	"Spain":                  "西班牙",
	"Brazil":                 "巴西",
	"Mexico":                 "墨西哥",
	"Turkey":                 "土耳其",
	"Sweden":                 "瑞典",
	"Switzerland":            "瑞士",
	"New Zealand":            "新西兰",
}

func normalizeCountry(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return ""
	}
	if v, ok := countryAlias[s]; ok {
		return v
	}
	return s
}

func (r *Resolver) resolveOnline(ip string) Result {
	url := strings.ReplaceAll(r.cfg.OnlineFallback, "{ip}", ip)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return Result{}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = body // 在线兜底解析可在此按 provider 实现
	return Result{}
}

// normalize 清洗 ip2region 返回值中的占位符 "0"
func normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return ""
	}
	return s
}

// isPrivateIP 判断是否内网/回环地址
func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return true
	}
	return false
}

// String 便于调试
func (r Result) String() string {
	return fmt.Sprintf("%s/%s/%s", r.Country, r.Province, r.City)
}
