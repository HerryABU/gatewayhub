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
	"gatewayhub/internal/models"
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

// isUpgrade 判断是否为 WebSocket 等协议升级请求。
func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Connection"), "Upgrade") && req.Header.Get("Upgrade") != ""
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
