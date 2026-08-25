package router

import (
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/auth"
	"gatewayhub/internal/handlers"
)

// Setup 构建 http.Handler：外层先做「子路径部署前缀」剥离（在 Gin 路由匹配之前），
// 内部为 Gin 引擎：安全中间件 + 建站向导守卫 + API + 静态/SPA + 反向代理兜底。
func Setup(h *handlers.Handler, dist fs.FS) http.Handler {
	r := gin.New()
	// 用极轻量 requestLogger 替代 gin.Logger()：gin.Logger 会同步向终端逐条输出，
	// 在 Windows 控制台下 I/O 极慢，是本地联调的主要拖慢点。默认(debug 以下)不打印逐请求日志。
	r.Use(requestLogger(h), gin.Recovery(), corsMiddleware())
	// 安全防护（DDoS/CC 限流、WAF、IP/API 黑白名单），建站向导路径豁免
	r.Use(securityMiddleware(h))

	api := r.Group("/api")
	{
		// 建站向导（仅未完成时可访问；完成后永久 404；默认限 localhost）
		setup := api.Group("/setup")
		setup.Use(setupLocalGuard(h))
		{
			setup.GET("/status", h.SetupStatus)
			setup.POST("/configure", h.SetupConfigure)
		}

		// 公开接口（读）
		api.GET("/settings", h.GetSettings)
		api.GET("/compliance", h.GetCompliance)

		// 需完成建站向导后方可访问
		gated := api.Group("")
		gated.Use(requireConfigured(h))
		{
			gated.POST("/auth/login", h.Login)
			gated.POST("/auth/refresh", h.Refresh)
			gated.GET("/routes", h.ListRoutes)

			admin := gated.Group("")
			admin.Use(jwtAuth(h.Cfg.JWT.Secret))
			{
				admin.PUT("/auth/password", h.ChangePassword)
				admin.PUT("/settings", h.UpdateSettings)

				// 路由管理
				admin.POST("/routes", h.CreateRoute)
				admin.PUT("/routes/:prefix", h.UpdateRoute)
				admin.DELETE("/routes/:prefix", h.DeleteRoute)
				admin.PATCH("/routes/:prefix/status", h.UpdateRouteStatus)

				// 统计与地理
				admin.GET("/stats/overview", h.StatsOverview)
				admin.GET("/stats/routes", h.StatsRoutes)
				admin.GET("/stats/trend", h.StatsTrend)
				admin.GET("/stats/geo", h.StatsGeo)
				admin.POST("/stats/cleanup", h.StatsCleanup)

				// 健康检查状态条
				admin.GET("/health", h.HealthStatus)
				admin.POST("/health/check", h.HealthCheckNow)

				// 安全防护：IP/API 黑白名单
				admin.GET("/security/ips", h.ListIPRules)
				admin.POST("/security/ips", h.CreateIPRule)
				admin.DELETE("/security/ips/:id", h.DeleteIPRule)
				admin.GET("/security/apis", h.ListAPIRules)
				admin.POST("/security/apis", h.CreateAPIRule)
				admin.DELETE("/security/apis/:id", h.DeleteAPIRule)

				// 数据库迁移
				admin.GET("/migrate/info", h.MigrateInfo)
				admin.POST("/migrate/test", h.MigrateTest)
				admin.POST("/migrate/run", h.MigrateRun)

				// 数据库备份
				admin.POST("/backup", h.BackupCreate)
				admin.GET("/backup/list", h.BackupList)
				admin.GET("/backup/download", h.BackupDownload)
				admin.DELETE("/backup/:id", h.BackupDelete)
			}
		}
	}

	fileServer := http.FileServer(http.FS(dist))
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "message": "not found"})
			return
		}
		// 未完成建站时，禁止代理转发，仅提供前端（向导页）
		if !h.IsConfigured() {
			serveSPA(c, dist, fileServer)
			return
		}
		if h.Proxy.Matchable(path, c.Request.Host) {
			h.Proxy.Handle(c)
			return
		}
		serveSPA(c, dist, fileServer)
	})

	// 外层包装：在 Gin 路由匹配之前智能剥离「子路径部署前缀」（详见 stripDeployPrefix）
	var engine http.Handler = r
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p := stripDeployPrefix(req.URL.Path, h); p != req.URL.Path {
			req.URL.Path = p
			req.URL.RawPath = ""
		}
		engine.ServeHTTP(w, req)
	})
}

// serveSPA 提供静态资源或 SPA 回退；多段未知路径返回 404
func serveSPA(c *gin.Context, dist fs.FS, fileServer http.Handler) {
	path := c.Request.URL.Path
	if p := strings.TrimPrefix(path, "/"); p != "" {
		if f, err := dist.Open(p); err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
	}
	if strings.Contains(strings.Trim(path, "/"), "/") {
		c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "message": "route not found"})
		return
	}
	c.Request.URL.Path = "/"
	fileServer.ServeHTTP(c.Writer, c.Request)
}

// reservedFirstSegments 与业务路由命名保留字一致：首段为这些值时绝不当作部署前缀剥离
var reservedFirstSegments = map[string]bool{
	"api": true, "assets": true, "static": true, "favicon": true,
}

// firstSeg 切分路径首段与其后部分（rest 恒以 / 开头，无第二段时 rest 为空）
func firstSeg(p string) (string, string) {
	t := strings.TrimPrefix(p, "/")
	if t == "" {
		return "", ""
	}
	if i := strings.Index(t, "/"); i >= 0 {
		return t[:i], "/" + t[i+1:]
	}
	return t, ""
}

// stripDeployPrefix 智能剥离「子路径部署前缀」，兼容自建反向代理 /{name}/ → 网关。
//
// 两种典型拓扑都能工作（严禁硬编码 {name}，前缀由反代决定）：
//  1. 反代剥离前缀（常见）：网关收到干净路径 /api、/assets、/login…… 首段为保留字或
//     无第二段，一律不剥离，行为与无前缀部署完全一致。
//  2. 反代透传前缀：网关收到 /{name}/api、/{name}/assets、/{name}/login……
//     仅当首段「既非业务路由（含多级前缀首段）、亦非保留字、且路径含第二段」时，
//     将该段判定为部署前缀并剥离，其余原样放行。
//
// 嵌套的业务路由同样受益：/{name}/java-order/login → /java-order/login → 命中业务路由。
// 必须在 Gin 路由匹配之前执行（Gin 的中间件在路由匹配之后才运行，改路径不会重新路由）。
func stripDeployPrefix(p string, h *handlers.Handler) string {
	seg, rest := firstSeg(p)
	if seg != "" && rest != "" && !reservedFirstSegments[seg] && !h.Proxy.HasPathPrefix(seg) {
		return rest
	}
	return p
}

// securityMiddleware 安全防护中间件（建站向导路径豁免）
func securityMiddleware(h *handlers.Handler) gin.HandlerFunc {
	sec := h.Security.Middleware()
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/setup/") {
			c.Next()
			return
		}
		sec(c)
	}
}

// requireConfigured 未完成建站时拦截（返回 428 要求先完成建站）
func requireConfigured(h *handlers.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.IsConfigured() {
			c.AbortWithStatusJSON(http.StatusPreconditionRequired, gin.H{
				"code":           428,
				"message":        "setup required",
				"setup_required": true,
			})
			return
		}
		c.Next()
	}
}

// setupLocalGuard 建站向导默认仅允许 localhost 访问（严防死守）
func setupLocalGuard(h *handlers.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.Cfg.Setup.AllowRemote {
			c.Next()
			return
		}
		ip := net.ParseIP(c.ClientIP())
		if ip != nil && ip.IsLoopback() {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "setup is local-only"})
	}
}

func jwtAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := ""
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimPrefix(h, "Bearer ")
		}
		claims, err := auth.ParseToken(secret, tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 1005, "message": "unauthorized"})
			return
		}
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// requestLogger 极轻量访问日志，替代 gin.Logger()。
// 默认（log.level 非 debug/trace）不打印逐请求日志——避免同步终端 I/O 拖慢转发；
// 仅记录 5xx 与耗时 ≥1s 的慢请求。需要全量请求日志时，将 config.yaml 的 log.level 设为 debug。
func requestLogger(h *handlers.Handler) gin.HandlerFunc {
	level := strings.ToLower(h.Cfg.Log.Level)
	verbose := level == "debug" || level == "trace"
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		cost := time.Since(start)
		if verbose {
			log.Printf("[http] %s %s -> %d (%s)",
				c.Request.Method, c.Request.URL.Path, status, cost.Round(time.Microsecond))
			return
		}
		if status >= 500 || cost >= time.Second {
			log.Printf("[http] SLOW/ERR %s %s -> %d (%s)",
				c.Request.Method, c.Request.URL.Path, status, cost.Round(time.Millisecond))
		}
	}
}
