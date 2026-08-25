package handlers

import (
	"github.com/gin-gonic/gin"

	"gatewayhub/internal/models"
)

// provinceAlias 将 ip2region 返回的省份名归一化为地图 GeoJSON 的标准名称
var provinceAlias = map[string]string{
	"北京":       "北京市",
	"北京市":      "北京市",
	"天津":       "天津市",
	"天津市":      "天津市",
	"上海":       "上海市",
	"上海市":      "上海市",
	"重庆":       "重庆市",
	"重庆市":      "重庆市",
	"河北":       "河北省",
	"河北省":      "河北省",
	"山西":       "山西省",
	"山西省":      "山西省",
	"内蒙古":      "内蒙古自治区",
	"内蒙古自治区":   "内蒙古自治区",
	"辽宁":       "辽宁省",
	"辽宁省":      "辽宁省",
	"吉林":       "吉林省",
	"吉林省":      "吉林省",
	"黑龙江":      "黑龙江省",
	"黑龙江省":     "黑龙江省",
	"江苏":       "江苏省",
	"江苏省":      "江苏省",
	"浙江":       "浙江省",
	"浙江省":      "浙江省",
	"安徽":       "安徽省",
	"安徽省":      "安徽省",
	"福建":       "福建省",
	"福建省":      "福建省",
	"江西":       "江西省",
	"江西省":      "江西省",
	"山东":       "山东省",
	"山东省":      "山东省",
	"河南":       "河南省",
	"河南省":      "河南省",
	"湖北":       "湖北省",
	"湖北省":      "湖北省",
	"湖南":       "湖南省",
	"湖南省":      "湖南省",
	"广东":       "广东省",
	"广东省":      "广东省",
	"广西":       "广西壮族自治区",
	"广西壮族自治区":  "广西壮族自治区",
	"海南":       "海南省",
	"海南省":      "海南省",
	"四川":       "四川省",
	"四川省":      "四川省",
	"贵州":       "贵州省",
	"贵州省":      "贵州省",
	"云南":       "云南省",
	"云南省":      "云南省",
	"西藏":       "西藏自治区",
	"西藏自治区":    "西藏自治区",
	"陕西":       "陕西省",
	"陕西省":      "陕西省",
	"甘肃":       "甘肃省",
	"甘肃省":      "甘肃省",
	"青海":       "青海省",
	"青海省":      "青海省",
	"宁夏":       "宁夏回族自治区",
	"宁夏回族自治区":  "宁夏回族自治区",
	"新疆":       "新疆维吾尔自治区",
	"新疆维吾尔自治区": "新疆维吾尔自治区",
	"台湾":       "台湾省",
	"台湾省":      "台湾省",
	"香港":       "香港特别行政区",
	"香港特别行政区":  "香港特别行政区",
	"澳门":       "澳门特别行政区",
	"澳门特别行政区":  "澳门特别行政区",
}

func normalizeProvince(p string) string {
	if p == "" {
		return ""
	}
	if v, ok := provinceAlias[p]; ok {
		return v
	}
	return p
}

type geoCount struct {
	Name  string `gorm:"column:name" json:"name"`
	Value int64  `gorm:"column:value" json:"value"`
}

type geoCity struct {
	Province string `gorm:"column:province" json:"province"`
	City     string `gorm:"column:city" json:"city"`
	Value    int64  `gorm:"column:value" json:"value"`
}

type geoOverseas struct {
	Code   string `gorm:"column:code" json:"code"`
	NameCN string `gorm:"column:name_cn" json:"name_cn"`
	Value  int64  `gorm:"column:value" json:"value"`
}

// countryCodeToEN ISO2 → 英文名（用于世界地图匹配）
var countryCodeToEN = map[string]string{
	"CN": "China", "US": "United States", "SG": "Singapore", "JP": "Japan",
	"KR": "South Korea", "GB": "United Kingdom", "DE": "Germany", "FR": "France",
	"RU": "Russia", "CA": "Canada", "AU": "Australia", "IN": "India",
	"MY": "Malaysia", "TH": "Thailand", "VN": "Vietnam", "ID": "Indonesia",
	"PH": "Philippines", "NL": "Netherlands", "IT": "Italy", "ES": "Spain",
	"BR": "Brazil", "MX": "Mexico", "TR": "Turkey", "SE": "Sweden",
	"CH": "Switzerland", "NZ": "New Zealand", "HK": "Hong Kong", "MO": "Macao",
	"TW": "Taiwan", "AE": "United Arab Emirates", "SA": "Saudi Arabia", "IL": "Israel",
	"ZA": "South Africa", "EG": "Egypt", "NG": "Nigeria", "AR": "Argentina",
	"CL": "Chile", "PT": "Portugal", "PL": "Poland", "UA": "Ukraine",
	"IE": "Ireland", "AT": "Austria", "BE": "Belgium", "NO": "Norway",
	"FI": "Finland", "DK": "Denmark", "CZ": "Czech Republic", "GR": "Greece",
	"IR": "Iran", "PK": "Pakistan", "BD": "Bangladesh", "KZ": "Kazakhstan",
}

// StatsGeo GET /api/stats/geo?days=7  —— 访问者地理位置分布（地图可视化数据）
func (h *Handler) StatsGeo(c *gin.Context) {
	days := parseDays(c, 7)
	start := startOfToday().AddDate(0, 0, -(days - 1))

	// 省级分布
	var provRows []geoCount
	h.DB.Raw(
		`SELECT province AS name, COUNT(*) AS value FROM access_logs
		 WHERE created_at >= ? AND country = '中国' AND province != ''
		 GROUP BY province ORDER BY value DESC`,
		start,
	).Scan(&provRows)

	provinces := make([]geoCount, 0, len(provRows))
	for _, p := range provRows {
		n := normalizeProvince(p.Name)
		if n == "" {
			continue
		}
		provinces = append(provinces, geoCount{Name: n, Value: p.Value})
	}

	// 市级分布（TOP N）
	var cityRows []geoCity
	h.DB.Raw(
		`SELECT province, city, COUNT(*) AS value FROM access_logs
		 WHERE created_at >= ? AND country = '中国' AND city != ''
		 GROUP BY province, city ORDER BY value DESC LIMIT 20`,
		start,
	).Scan(&cityRows)

	cities := make([]geoCity, 0, len(cityRows))
	for _, c := range cityRows {
		cities = append(cities, geoCity{Province: normalizeProvince(c.Province), City: c.City, Value: c.Value})
	}

	// 境外分布（按国家代码，附英文名供世界地图匹配）
	var ovRows []geoOverseas
	h.DB.Raw(
		`SELECT country_code AS code, country AS name_cn, COUNT(*) AS value FROM access_logs
		 WHERE created_at >= ? AND country != '' AND country != '中国'
		 GROUP BY country_code, country ORDER BY value DESC`,
		start,
	).Scan(&ovRows)

	overseas := make([]gin.H, 0, len(ovRows))
	for _, o := range ovRows {
		overseas = append(overseas, gin.H{
			"code":    o.Code,
			"name_cn": o.NameCN,
			"name_en": countryCodeToEN[o.Code],
			"value":   o.Value,
		})
	}

	var total, chinaTotal, overseasTotal int64
	h.DB.Model(&models.AccessLog{}).Where("created_at >= ?", start).Count(&total)
	h.DB.Model(&models.AccessLog{}).Where("created_at >= ? AND country = '中国'", start).Count(&chinaTotal)
	h.DB.Model(&models.AccessLog{}).Where("created_at >= ? AND country != '' AND country != '中国'", start).Count(&overseasTotal)

	h.ok(c, gin.H{
		"days":           days,
		"total":          total,
		"china_total":    chinaTotal,
		"overseas_total": overseasTotal,
		"provinces":      provinces,
		"cities":         cities,
		"overseas":       overseas,
		"geo_loaded":     h.Geo.Loaded(),
	})
}
