package security

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gatewayhub/internal/config"
	"gatewayhub/internal/models"
)

// ipRule 内存中的 IP 规则
type ipRule struct {
	ipNet  *net.IPNet
	action string
}

// rateState 单个 IP 的限流状态
type rateState struct {
	tokens     float64
	lastRefill time.Time
	exceed     int
	banUntil   time.Time
}

// Manager 安全防护管理器
type Manager struct {
	db    *gorm.DB
	cfg   config.SecurityConfig

	mu      sync.RWMutex
	ipRules []ipRule
	apiRules map[string]string // path -> action

	rlMu    sync.Mutex
	rl      map[string]*rateState
	global  float64
	globalLast time.Time

	// WAF 正则
	sqlPatterns  []*regexp.Regexp
	xssPatterns  []*regexp.Regexp
}

// New 创建安全管理器
func New(db *gorm.DB, cfg config.SecurityConfig) *Manager {
	m := &Manager{
		db:       db,
		cfg:      cfg,
		apiRules: make(map[string]string),
		rl:       make(map[string]*rateState),
	}
	m.compileWAF()
	m.Refresh()
	go m.cleanupLoop()
	return m
}

func (m *Manager) compileWAF() {
	sql := []string{
		`(?i)(union\s+(all\s+)?select)`,
		`(?i)(select\s+.*\s+from)`,
		`(?i)(insert\s+into)`,
		`(?i)(update\s+.*\s+set)`,
		`(?i)(delete\s+from)`,
		`(?i)(drop\s+(table|database))`,
		`(?i)(alter\s+table)`,
		`(?i)(or\s+['"]?\d*['"]?\s*=\s*['"]?\d*['"]?)`,
		`(?i)(sleep\s*\(|benchmark\s*\(|pg_sleep\s*\()`,
		`(?i)(information_schema|sys\.tables|xp_cmdshell)`,
		`(?i)(--\s|/\*|\*/|;\s*(drop|delete|update|insert)\s)`,
	}
	xss := []string{
		`(?i)(<script[^>]*>)`,
		`(?i)(javascript\s*:)`,
		`(?i)(onerror\s*=)`,
		`(?i)(onload\s*=)`,
		`(?i)(onclick\s*=)`,
		`(?i)(alert\s*\()`,
		`(?i)(document\.cookie)`,
		`(?i)(<iframe|<img[^>]+on)`,
		`(?i)(vbscript\s*:)`,
		`(?i)(eval\s*\()`,
		`(?i)(<svg[^>]*onload)`,
	}
	for _, p := range sql {
		if re, err := regexp.Compile(p); err == nil {
			m.sqlPatterns = append(m.sqlPatterns, re)
		}
	}
	for _, p := range xss {
		if re, err := regexp.Compile(p); err == nil {
			m.xssPatterns = append(m.xssPatterns, re)
		}
	}
}

// Refresh 从数据库重新加载黑白名单规则
func (m *Manager) Refresh() {
	var ips []models.IPRule
	var apis []models.APIRule
	m.db.Find(&ips)
	m.db.Find(&apis)

	m.mu.Lock()
	m.ipRules = m.ipRules[:0]
	for _, r := range ips {
		if _, ipNet, err := net.ParseCIDR(r.IP); err == nil {
			m.ipRules = append(m.ipRules, ipRule{ipNet: ipNet, action: r.Action})
		} else if ip := net.ParseIP(r.IP); ip != nil {
			// 单个 IP 转 /32 或 /128
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			m.ipRules = append(m.ipRules, ipRule{ipNet: &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, action: r.Action})
		}
	}
	m.apiRules = make(map[string]string, len(apis))
	for _, r := range apis {
		m.apiRules[r.Path] = r.Action
	}
	m.mu.Unlock()
}

// checkIP 判断 IP 是否被允许（deny 优先，其次 allow 白名单）
func (m *Manager) checkIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	hasAllow := false
	for _, r := range m.ipRules {
		if r.action == models.ActionAllow {
			hasAllow = true
		}
	}
	allowed := true
	denied := false
	for _, r := range m.ipRules {
		if r.ipNet.Contains(ip) {
			if r.action == models.ActionDeny {
				denied = true
			} else {
				allowed = true
			}
		}
	}
	if denied {
		return false
	}
	if hasAllow && !allowed {
		return false
	}
	return true
}

// checkAPI 判断 API 路径是否被允许
func (m *Manager) checkAPI(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.apiRules) == 0 {
		return true
	}
	// 最精确匹配
	best := ""
	for p := range m.apiRules {
		if strings.HasPrefix(path, p) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return true // 未命中任何规则，默认放行
	}
	return m.apiRules[best] == models.ActionAllow
}

// allow 令牌桶限流 + 自动封禁
func (m *Manager) allow(ip string) bool {
	if m.cfg.RateLimit <= 0 {
		return true
	}
	m.rlMu.Lock()
	defer m.rlMu.Unlock()
	st, ok := m.rl[ip]
	now := time.Now()
	if !ok {
		st = &rateState{tokens: float64(m.cfg.Burst), lastRefill: now}
		m.rl[ip] = st
	}
	// 已封禁
	if now.Before(st.banUntil) {
		return false
	}
	// 补充令牌
	elapsed := now.Sub(st.lastRefill).Seconds()
	st.tokens += elapsed * float64(m.cfg.RateLimit)
	if st.tokens > float64(m.cfg.Burst) {
		st.tokens = float64(m.cfg.Burst)
	}
	st.lastRefill = now

	if st.tokens >= 1 {
		st.tokens--
		st.exceed = 0
		return true
	}
	st.exceed++
	if st.exceed >= m.cfg.BanThreshold {
		st.banUntil = now.Add(time.Duration(m.cfg.BanDuration) * time.Second)
		st.exceed = 0
	}
	return false
}

// allowGlobal 全局限流
func (m *Manager) allowGlobal() bool {
	if m.cfg.GlobalRPS <= 0 {
		return true
	}
	m.rlMu.Lock()
	defer m.rlMu.Unlock()
	now := time.Now()
	elapsed := now.Sub(m.globalLast).Seconds()
	m.global += elapsed * float64(m.cfg.GlobalRPS)
	if m.global > float64(m.cfg.GlobalRPS) {
		m.global = float64(m.cfg.GlobalRPS)
	}
	m.globalLast = now
	if m.global >= 1 {
		m.global--
		return true
	}
	return false
}

// wafCheck 检测 SQL/XSS 攻击
func (m *Manager) wafCheck(input string) bool {
	for _, re := range m.sqlPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	for _, re := range m.xssPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

// ClientIP 提取真实客户端 IP
func (m *Manager) ClientIP(c *gin.Context) string {
	if m.cfg.TrustProxies {
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
		if xri := c.GetHeader("X-Real-IP"); xri != "" {
			return xri
		}
	}
	if host, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		return host
	}
	return c.Request.RemoteAddr
}

// Middleware 安全防护中间件
func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.cfg.Enabled {
			c.Next()
			return
		}
		ip := m.ClientIP(c)

		// 1. IP 黑白名单
		if !m.checkIP(ip) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": http.StatusForbidden, "message": "IP forbidden"})
			return
		}
		// 2. 全局限流
		if !m.allowGlobal() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": http.StatusTooManyRequests, "message": "too many requests"})
			return
		}
		// 3. 每 IP 限流（DDoS/CC）
		if !m.allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": http.StatusTooManyRequests, "message": "rate limit exceeded"})
			return
		}
		// 4. API 黑白名单
		if strings.HasPrefix(c.Request.URL.Path, "/api/") && !m.checkAPI(c.Request.URL.Path) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": http.StatusForbidden, "message": "api forbidden"})
			return
		}
		// 5. WAF SQL/XSS 拦截（对解码后的 URL/查询参数/请求体检测）
		if m.cfg.WAFEnabled {
			if m.wafCheckURL(c) ||
				m.wafCheck(c.Request.UserAgent()) ||
				m.wafCheck(c.GetHeader("Referer")) ||
				m.wafCheck(inspectBody(c, m.cfg.WAFBodyMax)) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": http.StatusForbidden, "message": "request blocked by WAF"})
				return
			}
		}
		c.Next()
	}
}

// wafCheckURL 对解码后的路径与查询参数做 WAF 检测。
// 无查询串时直接只扫路径，避免 URL.Query() 每次分配 map 的开销。
func (m *Manager) wafCheckURL(c *gin.Context) bool {
	if m.wafCheck(c.Request.URL.Path) {
		return true
	}
	if c.Request.URL.RawQuery == "" {
		return false
	}
	for key, vals := range c.Request.URL.Query() {
		if m.wafCheck(key) {
			return true
		}
		for _, v := range vals {
			if m.wafCheck(v) {
				return true
			}
		}
	}
	return false
}

// inspectBody 读取请求体用于 WAF 检测，检测后还原 body 供后续 handler 使用。
// 性能与正确性设计：
//   - GET/HEAD/OPTIONS/DELETE/TRACE 等无请求体方法直接跳过。
//   - multipart/form-data（文件上传）与其它二进制 Content-Type 跳过扫描，原样留给后端。
//   - 已知 Content-Length 超过上限（max）时完全跳过读取，body 保持流式透传。
//   - 未知长度（chunked）最多读 max+1 字节；未超限则整体读入，超限则把已读前缀与剩余 body 拼接还原，
//     彻底修复旧实现「>1MB 请求体被 LimitReader 静默截断」的 bug。
func inspectBody(c *gin.Context, max int) string {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodDelete, http.MethodTrace:
		return ""
	}
	ct := c.GetHeader("Content-Type")
	if !strings.Contains(ct, "application/x-www-form-urlencoded") &&
		!strings.Contains(ct, "application/json") {
		return ""
	}
	if max <= 0 {
		max = 512 * 1024
	}
	if c.Request.ContentLength > int64(max) {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(max)+1))
	if err != nil {
		return ""
	}
	if len(data) > max {
		// 超限：跳过扫描，但把已读前缀与剩余 body 完整还原给下游
		c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(data), c.Request.Body))
		return ""
	}
	// 未超限：整个 body 已读尽，原样还原
	c.Request.Body = io.NopCloser(bytes.NewReader(data))
	return string(data)
}

// cleanupLoop 定期清理过期的限流状态
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.rlMu.Lock()
		now := time.Now()
		for ip, st := range m.rl {
			if now.Sub(st.lastRefill) > 10*time.Minute && now.After(st.banUntil) {
				delete(m.rl, ip)
			}
		}
		m.rlMu.Unlock()
	}
}
