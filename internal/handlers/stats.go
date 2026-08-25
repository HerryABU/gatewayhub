package handlers

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/models"
	"gatewayhub/internal/stats"
)

func parseDays(c *gin.Context, def int) int {
	d := def
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 90 {
			d = n
		}
	}
	return d
}

func startOfToday() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// StatsOverview GET /api/stats/overview
func (h *Handler) StatsOverview(c *gin.Context) {
	var totalPV, todayPV, totalRoutes int64
	h.DB.Model(&models.AccessLog{}).Count(&totalPV)
	h.DB.Model(&models.AccessLog{}).Where("created_at >= ?", startOfToday()).Count(&todayPV)
	h.DB.Model(&models.Route{}).Count(&totalRoutes)

	var routes []models.Route
	h.DB.Find(&routes)
	healthMap := h.Health.StatusMap()
	healthy := 0
	for _, r := range routes {
		if healthMap[r.Prefix] == models.HealthHealthy {
			healthy++
		}
	}
	healthRate := 0.0
	if totalRoutes > 0 {
		healthRate = float64(healthy) / float64(totalRoutes) * 100
	}
	h.ok(c, gin.H{
		"total_pv":      totalPV,
		"today_pv":      todayPV,
		"total_routes":  totalRoutes,
		"healthy_routes": healthy,
		"health_rate":   healthRate,
	})
}

// dailyTrend 计算某路由近 N 天的逐日请求数（按时间升序）
// 采用 Go 侧分桶，避免依赖 SQLite date() 函数的时区/格式差异
func (h *Handler) dailyTrend(prefix string, days int) ([]string, []int64) {
	labels := make([]string, days)
	values := make([]int64, days)
	today := startOfToday()
	start := today.AddDate(0, 0, -(days - 1))

	var rows []struct {
		CreatedAt time.Time
	}
	h.DB.Model(&models.AccessLog{}).
		Select("created_at").
		Where("route_prefix = ? AND created_at >= ? AND created_at < ?", prefix, start, today.AddDate(0, 0, 1)).
		Scan(&rows)

	for _, r := range rows {
		idx := daysBetween(r.CreatedAt, start)
		if idx < 0 {
			idx = 0
		}
		if idx >= days {
			idx = days - 1
		}
		values[idx]++
	}
	for i := 0; i < days; i++ {
		labels[i] = start.AddDate(0, 0, i).Format("01-02")
	}
	return labels, values
}

// daysBetween 计算两个时间点之间相差的天数（按本地日期，DST 安全）
func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	adn := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	bdn := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(adn.Sub(bdn).Hours() / 24)
}

// StatsRoutes GET /api/stats/routes?days=7
func (h *Handler) StatsRoutes(c *gin.Context) {
	days := parseDays(c, 7)
	today := startOfToday()
	yesterday := today.AddDate(0, 0, -1)

	var routes []models.Route
	h.DB.Order("id asc").Find(&routes)

	type routeStat struct {
		Prefix       string  `json:"prefix"`
		Name         string  `json:"name"`
		TotalPV      int64   `json:"total_pv"`
		TodayPV      int64   `json:"today_pv"`
		YesterdayPV  int64   `json:"yesterday_pv"`
		Trend        []int64 `json:"trend"`
	}

	result := make([]routeStat, 0, len(routes))
	for _, r := range routes {
		var totalPV, todayPV, yesterdayPV int64
		h.DB.Model(&models.AccessLog{}).Where("route_prefix = ?", r.Prefix).Count(&totalPV)
		h.DB.Model(&models.AccessLog{}).Where("route_prefix = ? AND created_at >= ?", r.Prefix, today).Count(&todayPV)
		h.DB.Model(&models.AccessLog{}).
			Where("route_prefix = ? AND created_at >= ? AND created_at < ?", r.Prefix, yesterday, today).
			Count(&yesterdayPV)
		_, trend := h.dailyTrend(r.Prefix, days)
		result = append(result, routeStat{
			Prefix:      r.Prefix,
			Name:        r.Name,
			TotalPV:     totalPV,
			TodayPV:     todayPV,
			YesterdayPV: yesterdayPV,
			Trend:       trend,
		})
	}
	h.ok(c, gin.H{"routes": result, "days": days})
}

// StatsTrend GET /api/stats/trend?prefix=xxx&days=7
func (h *Handler) StatsTrend(c *gin.Context) {
	prefix := c.Query("prefix")
	if prefix == "" {
		h.fail(c, 1001, "缺少 prefix 参数")
		return
	}
	days := parseDays(c, 7)
	labels, values := h.dailyTrend(prefix, days)
	var total, count int64
	for _, v := range values {
		total += v
		if v > 0 {
			count++
		}
	}
	avg := 0.0
	if count > 0 {
		avg = float64(total) / float64(count)
	}
	h.ok(c, gin.H{"labels": labels, "values": values, "total": total, "avg": avg})
}

// StatsCleanup POST /api/stats/cleanup
func (h *Handler) StatsCleanup(c *gin.Context) {
	var req struct {
		RetainDays int `json:"retain_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.RetainDays = 0
	}
	retain := req.RetainDays
	if retain <= 0 {
		retain = h.Cfg.Stats.RetainDays
	}
	deleted, err := stats.Cleanup(h.DB, retain)
	if err != nil {
		h.fail(c, 2001, "清理失败")
		return
	}
	var remaining int64
	h.DB.Model(&models.AccessLog{}).Count(&remaining)
	h.ok(c, gin.H{"deleted_count": deleted, "remaining_count": remaining})
}
