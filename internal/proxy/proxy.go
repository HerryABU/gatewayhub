package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/models"
	"gatewayhub/internal/stats"
	"gatewayhub/internal/target"
)

// bufferPool 复用 io.Copy 缓冲区，减少高并发下的内存分配与 GC 压力。
var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

type poolBuffer struct{}

func (poolBuffer) Get() []byte  { return *bufferPool.Get().(*[]byte) }
func (poolBuffer) Put(b []byte) { bufferPool.Put(&b) }

// Entry 路由表条目：同一后端地址共享一个 Transport 连接池，
// 路径模式（Proxy，剥离前缀 + 响应重写）与子域名模式（SubProxy，零重写纯透明）各自独立的 ReverseProxy。
type Entry struct {
	Route    models.Route
	Parsed   target.Parsed
	Proxy    *httputil.ReverseProxy // 路径模式
	SubProxy *httputil.ReverseProxy // 子域名模式
}

// Manager 反向代理管理器（内存路由表 + 热加载 + 子域名/路径双模式）
type Manager struct {
	db         *gorm.DB
	stats      *stats.Writer
	fileLog    *accesslog.FileLogger // 访问日志落盘（含请求头），可为 nil
	baseDomain string               // 子域名后缀（如 localhost），空则禁用子域名路由
	mu         sync.RWMutex
	m          map[string]*Entry
	multi      []string // 含 "/" 的多级前缀，按长度降序（最长前缀匹配用）
}

// NewManager 创建代理管理器
func NewManager(db *gorm.DB, stats *stats.Writer, baseDomain string) *Manager {
	return &Manager{db: db, stats: stats, baseDomain: baseDomain, m: make(map[string]*Entry)}
}

// SetFileLogger 注入访问日志落盘器（含请求头，按天/小时）
func (m *Manager) SetFileLogger(l *accesslog.FileLogger) {
	m.fileLog = l
}

// Load 从数据库加载全部路由到内存
func (m *Manager) Load() error {
	var routes []models.Route
	if err := m.db.Find(&routes).Error; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m = make(map[string]*Entry, len(routes))
	for _, r := range routes {
		if e, err := m.buildEntry(r); err == nil {
			m.m[r.Prefix] = e
		}
	}
	m.rebuildMultiLocked()
	return nil
}

func (m *Manager) buildEntry(r models.Route) (*Entry, error) {
	p, err := target.Parse(r.Target)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(r.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// 两个代理共享同一 Transport，复用后端连接池
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
	}
	entry := &Entry{Route: r, Parsed: p}

	// 路径模式：剥离 /prefix 前缀 + 响应内容重写（加回前缀、注入 <base>）
	rw := newRewriter(r.Prefix, p.Root, p.Host)
	entry.Proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// 记录网关侧原始路径（外链 CSS 相对化需要推算基准目录）
			req = withGWPath(req, req.URL.Path)
			rest := sanitizeRest(req.URL.Path, r.Prefix)
			applyForwardHeaders(req, p)
			req.URL.Path = target.JoinPath(p.Root, rest)
			// 仅接受 gzip，便于在 ModifyResponse 中解压重写后再压缩
			req.Header.Set("Accept-Encoding", "gzip")
		},
		Transport:      transport,
		ModifyResponse: rw.modify,
		FlushInterval:  -1,
		BufferPool:     poolBuffer{},
		ErrorHandler:   m.errorHandler,
	}

	// 子域名模式：前缀在 Host 头里，路径即后端根路径，零重写、零解压，纯透明转发（速度=源站）
	entry.SubProxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			applyForwardHeaders(req, p)
			req.URL.Path = target.JoinPath(p.Root, req.URL.Path)
			// 不设置 Accept-Encoding：保留原始值，让后端按需 gzip/br，网关零解压透传
		},
		Transport:      transport,
		FlushInterval:  -1,
		BufferPool:     poolBuffer{},
		ErrorHandler:   m.errorHandler,
	}
	return entry, nil
}

// errorHandler 统一的后端错误响应（502/504）
func (m *Manager) errorHandler(w http.ResponseWriter, req *http.Request, err error) {
	code := http.StatusBadGateway
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		code = http.StatusGatewayTimeout
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"code":%d,"message":"%s"}`, code, http.StatusText(code))
}

// applyForwardHeaders 设置后端目标并透传/注入转发头（两模式共用）
func applyForwardHeaders(req *http.Request, p target.Parsed) {
	req.URL.Scheme = p.Scheme
	req.URL.Host = p.Host
	req.Host = p.Host

	clientIP := clientIPFrom(req.RemoteAddr, req.Header)
	if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
		req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
	} else {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	req.Header.Set("X-Real-IP", clientIP)
	proto := "http"
	if req.TLS != nil {
		proto = "https"
	}
	req.Header.Set("X-Forwarded-Proto", proto)
	// 完全透明：清除前缀痕迹，后端感知不到 /prefix 的存在
	req.Header.Del("X-Forwarded-Prefix")
}

// Upsert 新增或更新路由（写库 + 热加载）
func (m *Manager) Upsert(r models.Route) error {
	e, err := m.buildEntry(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.m[r.Prefix] = e
	m.rebuildMultiLocked()
	m.mu.Unlock()
	return nil
}

// Remove 从内存路由表删除
func (m *Manager) Remove(prefix string) {
	m.mu.Lock()
	delete(m.m, prefix)
	m.rebuildMultiLocked()
	m.mu.Unlock()
}

// Get 查找路由
func (m *Manager) Get(prefix string) (*Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.m[prefix]
	return e, ok
}

// Exists 判断前缀是否已存在
func (m *Manager) Exists(prefix string) bool {
	_, ok := m.Get(prefix)
	return ok
}

// HasPathPrefix 判断某「首段」是否为已注册业务路由（含多级前缀的首段，如 v2/beta 的 v2）。
// 供子路径部署前缀剥离逻辑使用：首段命中业务路由时不得剥离。
func (m *Manager) HasPathPrefix(first string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.m[first]; ok {
		return true
	}
	for _, p := range m.multi {
		if strings.HasPrefix(p, first+"/") {
			return true
		}
	}
	return false
}

// Count 返回路由数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.m)
}

// rebuildMultiLocked 重建「含 / 的多级前缀」降序列表（调用方需持有写锁）
func (m *Manager) rebuildMultiLocked() {
	multi := make([]string, 0)
	for p := range m.m {
		if strings.Contains(p, "/") {
			multi = append(multi, p)
		}
	}
	sort.Slice(multi, func(i, j int) bool { return len(multi[i]) > len(multi[j]) })
	m.multi = multi
}

// Matchable 判断请求是否命中任何路由（子域名或路径），供 NoRoute 决定转发 or SPA 回退。
func (m *Manager) Matchable(path, host string) bool {
	if prefix, ok := m.matchSubdomain(host); ok {
		if e, ok := m.Get(prefix); ok && e.Route.Status == models.StatusActive {
			return true
		}
	}
	_, _, ok := m.matchPath(path)
	return ok
}

// Handle 转发处理入口（供路由 NoRoute 调用）
// 匹配优先级：子域名（Host 头，零重写）→ 路径最长前缀（需重写）。
func (m *Manager) Handle(c *gin.Context) {
	path := c.Request.URL.Path
	clientIP := clientIPFrom(c.Request.RemoteAddr, c.Request.Header)

	// 1) 子域名路由：{prefix}.{base_domain}
	if prefix, ok := m.matchSubdomain(c.Request.Host); ok {
		if entry, ok := m.Get(prefix); ok {
			if entry.Route.Status != models.StatusActive {
				c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "message": "route disabled"})
				return
			}
			m.serve(c, entry, entry.SubProxy, prefix, path, clientIP)
			return
		}
	}

	// 2) 路径路由：最长前缀匹配
	prefix, entry, ok := m.matchPath(path)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "message": "route not found"})
		return
	}
	if entry.Route.Status != models.StatusActive {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "message": "route disabled"})
		return
	}
	m.serve(c, entry, entry.Proxy, prefix, path, clientIP)
}

// serve 执行转发并异步记录统计与访问日志（含请求头）
func (m *Manager) serve(c *gin.Context, entry *Entry, p *httputil.ReverseProxy, prefix, path, clientIP string) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: c.Writer, status: http.StatusOK}
	req := c.Request

	if isUpgrade(req) {
		// WebSocket：跳过请求超时，避免长连接被掐断
		p.ServeHTTP(sw, req)
	} else {
		ctx, cancel := context.WithTimeout(req.Context(), time.Duration(entry.Route.Timeout)*time.Second)
		defer cancel()
		p.ServeHTTP(sw, req.WithContext(ctx))
	}

	elapsed := time.Since(start).Milliseconds()
	m.stats.Enqueue(models.AccessLog{
		RoutePrefix:  prefix,
		RequestPath:  path,
		Method:       req.Method,
		StatusCode:   sw.status,
		ClientIP:     clientIP,
		UserAgent:    req.UserAgent(),
		ResponseTime: int(elapsed),
	})

	// 访问日志落盘（含完整请求头，按天/小时分文件）
	if m.fileLog != nil {
		headers := make(map[string]string, len(req.Header))
		for k, vs := range req.Header {
			headers[k] = strings.Join(vs, ", ")
		}
		if err := m.fileLog.Log(accesslog.Entry{
			Method:      req.Method,
			Path:        path,
			Status:      sw.status,
			LatencyMs:   elapsed,
			ClientIP:    clientIP,
			UserAgent:   req.UserAgent(),
			RoutePrefix: prefix,
			Headers:     headers,
		}); err != nil {
			log.Printf("[accesslog] write failed: %v", err)
		}
	}
}

// matchSubdomain 从 Host 头提取单段子域名前缀，如 "java-order.localhost[:8088]" → "java-order"。
func (m *Manager) matchSubdomain(host string) (string, bool) {
	if m.baseDomain == "" {
		return "", false
	}
	h := stripHostPort(host)
	suffix := "." + m.baseDomain
	if !strings.HasSuffix(strings.ToLower(h), strings.ToLower(suffix)) {
		return "", false
	}
	prefix := h[:len(h)-len(suffix)]
	if prefix == "" || strings.Contains(prefix, ".") {
		return "", false // 仅支持单段子域名
	}
	return strings.ToLower(prefix), true
}

// matchPath 最长前缀匹配：多级前缀（含 /）优先，其次第一段精确匹配（O(1) 快速路径）。
func (m *Manager) matchPath(path string) (string, *Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 多级前缀：长度降序，段边界对齐（最长优先）
	for _, p := range m.multi {
		if matchesPrefix(path, p) {
			if e, ok := m.m[p]; ok {
				return p, e, true
			}
		}
	}
	// 单段前缀：第一段精确匹配
	first := FirstSegment(path)
	if e, ok := m.m[first]; ok {
		return first, e, true
	}
	return "", nil, false
}

// stripHostPort 去掉 Host 头中的端口部分（无端口则原样返回）。
func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// isUpgrade 判断是否为 WebSocket 等协议升级请求。
func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Connection"), "Upgrade") && req.Header.Get("Upgrade") != ""
}

// FirstSegment 提取 URL 第一段路径作为转发名
func FirstSegment(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if i := strings.Index(trimmed, "/"); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// matchesPrefix 判断 path 是否命中 prefix（完整路径段边界：/prefix 或 /prefix/...）。
func matchesPrefix(path, prefix string) bool {
	full := "/" + prefix
	if !strings.HasPrefix(path, full) {
		return false
	}
	rest := strings.TrimPrefix(path, full)
	return rest == "" || rest[0] == '/'
}

// sanitizeRest 剥离路由前缀并清理路径，防止 `..` 路径穿透。
func sanitizeRest(pathStr, prefix string) string {
	if !matchesPrefix(pathStr, prefix) {
		return "/"
	}
	rest := strings.TrimPrefix(pathStr, "/"+prefix)
	hadTrailing := strings.HasSuffix(rest, "/")
	cleaned := path.Clean("/" + rest)
	if hadTrailing && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

// clientIPFrom 从 RemoteAddr 与请求头提取真实 IP
func clientIPFrom(remoteAddr string, header http.Header) string {
	if xff := header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if xri := header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// statusWriter 捕获响应状态码
type statusWriter struct {
	gin.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	return w.ResponseWriter.Write(b)
}
