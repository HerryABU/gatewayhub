package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/hopguard"
	"gatewayhub/internal/models"
	"gatewayhub/internal/proxy/rewrite"
)

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

// Handle 转发处理入口（供路由 NoRoute 调用）
// 匹配优先级：子域名（Host 头，零重写）→ 路径最长前缀（需重写）。
func (m *Manager) Handle(c *gin.Context) {
	path := c.Request.URL.Path
	clientIP := clientIPFrom(c.Request.RemoteAddr, c.Request.Header)

	// 1) 子域名路由：{prefix}.{base_domain}
	if prefix, ok := m.matchSubdomain(c.Request.Host); ok {
		if entry, ok := m.Get(prefix); ok {
			if entry.Route.Status != models.StatusActive {
				m.logRejected(c, prefix, kindSubdomain, http.StatusServiceUnavailable)
				c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "message": "route disabled"})
				return
			}
			m.serve(c, entry, entry.SubProxy, prefix, path, clientIP, kindSubdomain)
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
		m.logRejected(c, prefix, kindPath, http.StatusServiceUnavailable)
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "message": "route disabled"})
		return
	}
	m.serve(c, entry, entry.Proxy, prefix, path, clientIP, kindPath)
}

// 访问日志里的 kind 取值（网关扩展字段，NVS 侧没有这个字段）
const (
	kindPath      = "path"      // 路径式反代（剥离前缀 + 响应重写）
	kindSubdomain = "subdomain" // 子域名反代（零重写）
	kindIsolation = "isolation" // 路径隔离网关
)

// serve 执行转发并异步记录统计与访问日志（含请求头）
func (m *Manager) serve(c *gin.Context, entry *Entry, p *httputil.ReverseProxy, prefix, path, clientIP, kind string) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: c.Writer, status: http.StatusOK}
	req := c.Request
	// 改写审计载体：ModifyResponse 通过 resp.Request context 回填，
	// 转发结束后由本函数统一落盘，不在响应头里暴露内部信息。
	rewriteAudit := &rewrite.Audit{}
	req = rewrite.WithAudit(req, rewriteAudit)

	// 请求体片段：必须在转发前嗅探（转发会消费 Body），SniffBody 会完整还原读流
	var body string
	if m.fileLog.BodyEnabled() {
		body = accesslog.SniffBody(req)
	}

	// 自指/嵌套的链式转发：跳数超上限直接 508，不再把请求推给自己。
	// 这是「允许自指」的前提 —— 没有上限的自指就是一个请求生成器。
	hops, ok := m.hops.Next(req.Header)
	if !ok {
		m.logAccess(req, prefix, kind, http.StatusLoopDetected, 0, 0, clientIP, body)
		c.JSON(http.StatusLoopDetected, gin.H{
			"code": http.StatusLoopDetected,
			"message": fmt.Sprintf("loop detected: 转发跳数超过上限 %d（自指路由 %s 可能绕回了自己）",
				m.hops.Max(), prefix),
		})
		return
	}
	hopguard.Stamp(req.Header, hops)

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

	m.logAccess(req, prefix, kind, sw.status, sw.bytes, elapsed, clientIP, body)
	m.logRewrite(req, prefix, kind, rewriteAudit)
}

// logRewrite 落盘一条改写审计（log/rewrite/<date>/<HH>.log）。
// 只有真的改写过、或明确跳过（超限/编码不支持）才记，避免每条静态请求都刷日志。
func (m *Manager) logRewrite(req *http.Request, prefix, kind string, a *rewrite.Audit) {
	if m.fileLog == nil || a == nil || (a.Hits == 0 && a.Skip == "") {
		return
	}
	err := m.fileLog.LogCategory(accesslog.CategoryRewrite, accesslog.Entry{
		Method:        req.Method,
		Path:          req.URL.Path,
		Query:         req.URL.RawQuery,
		IP:            clientIPFrom(req.RemoteAddr, req.Header),
		UA:            req.UserAgent(),
		RoutePrefix:   prefix,
		Kind:          kind,
		RewriteHits:   a.Hits,
		RewriteChain:  a.Chain,
		RewriteSkip:   a.Skip,
		RewriteKind:   a.Kind,
		RewriteStatus: a.Status,
	})
	if err != nil {
		log.Printf("[accesslog] rewrite audit failed: %v", err)
	}
}

// logRejected 记录「命中路由但被拒」的请求（路由已禁用等），保证访问日志不缺口。
func (m *Manager) logRejected(c *gin.Context, prefix, kind string, status int) {
	if m.fileLog == nil {
		return
	}
	req := c.Request
	var body string
	if m.fileLog.BodyEnabled() {
		body = accesslog.SniffBody(req)
	}
	m.logAccess(req, prefix, kind, status, 0, 0, clientIPFrom(req.RemoteAddr, req.Header), body)
}

// logAccess 落盘一条访问记录（字段名与 NVS 对齐，另加 route_prefix / kind）。
func (m *Manager) logAccess(req *http.Request, prefix, kind string, status int, size int64, latencyMs int64, clientIP, body string) {
	if m.fileLog == nil {
		return
	}
	err := m.fileLog.Log(accesslog.Entry{
		Method:      req.Method,
		Path:        req.URL.Path,
		Query:       req.URL.RawQuery,
		IP:          clientIP,
		UA:          req.UserAgent(),
		Status:      status,
		Size:        size,
		Latency:     (time.Duration(latencyMs) * time.Millisecond).String(),
		Headers:     accesslog.HeadersOf(req.Header),
		Body:        body,
		RoutePrefix: prefix,
		Kind:        kind,
	})
	if err != nil {
		log.Printf("[accesslog] write failed: %v", err)
	}
}

// isUpgrade 判断是否为 WebSocket 等协议升级请求。
func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Connection"), "Upgrade") && req.Header.Get("Upgrade") != ""
}

// statusWriter 捕获响应状态码与写出字节数
type statusWriter struct {
	gin.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}
