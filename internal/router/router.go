package router

import (
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/auth"
	"gatewayhub/internal/handlers"
	isohandlers "gatewayhub/internal/handlers/isolation"
	"gatewayhub/internal/proxy"
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
		api.GET("/friend-links", h.ListFriendLinks) // 友链：纯跳转，不经过反向代理

		// 需完成建站向导后方可访问
		gated := api.Group("")
		gated.Use(requireConfigured(h))
		{
			gated.POST("/auth/login", h.Login)
			gated.POST("/auth/refresh", h.Refresh)
			// 路由列表对访客开放（主页展示），但需识别身份以过滤停用路由/后端地址。
			// optionalAuth 解析成功注入 username，失败也放行（返回访客视角）。
			gated.GET("/routes", optionalAuth(h.Cfg.JWT.Secret), h.ListRoutes)

			admin := gated.Group("")
			admin.Use(jwtAuth(h.Cfg.JWT.Secret))
			{
				admin.PUT("/auth/password", h.ChangePassword)
				admin.PUT("/settings", h.UpdateSettings)

				// 友链管理（仅改展示链接，不创建反向代理路由）
				admin.POST("/friend-links", h.CreateFriendLink)
				admin.PUT("/friend-links/:id", h.UpdateFriendLink)
				admin.DELETE("/friend-links/:id", h.DeleteFriendLink)

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

				// 路径隔离网关：配置/状态/指标/降级/热加载/审计
				iso := isohandlers.New(h.Iso, h.Cfg.Isolation.File)
				admin.POST("/isolation/probe", iso.Probe)
				admin.GET("/isolation/services", iso.List)
				admin.GET("/isolation/metrics", iso.Metrics)
				admin.GET("/isolation/audit", iso.Audit)
				admin.PATCH("/isolation/services/:name/mode", iso.SetMode)
				admin.POST("/isolation/reload", iso.Reload)

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
		// 越界归因：这条请求不归任何隔离服务，但可能正是某服务后端前端「没配 base path」
		// 打出来的根路径请求（/assets/*、/api/*）。只记指标与诊断，不改变去向。
		if h.Iso != nil && h.Iso.Active() && !h.Iso.Matches(path) {
			h.Iso.AttributeUnmatched(path)
		}
		// 官方域名入口：Host 精确匹配某条 active 路由的 target host（iframe 透传基础）。
		// 用户把官方域名 DNS/Hosts 指向网关后，浏览器访问的就是真正的官方 hostname，
		// 所有按 hostname 判环境的 SDK 天然走生产配置，无需逐站 hook。
		if _, _, ok := h.Proxy.MatchTargetHost(c.Request.Host); ok {
			h.Proxy.Handle(c)
			return
		}
		// 子域名路由优先：Host 已经声明归属某个后端，路径就是后端根路径。
		// 此时网关自己的 /api 守卫、静态资源与 SPA 回退都必须让位（否则后端
		// 的 /api/*、/assets/* 会被网关吞掉）。
		if h.Proxy.MatchableHost(c.Request.Host) {
			h.Proxy.Handle(c)
			return
		}
		// 应用内 SDK（字节 ttwid / 各类 OAuth SDK）常在 JS 里硬编码根相对路径
		// （/ttwid/check/、/api/user/info），且用原生 fetch 绕过 shim hook →
		// 打到网关根 404。智能回退：若 Referer 同源且指向某个已启用路由前缀，
		// 就把请求补上该前缀再转发。只认同源 Referer，避免跨站借道。
		if !h.Proxy.Matchable(path, c.Request.Host) {
			if prefix := prefixFromReferer(c, h.Proxy); prefix != "" {
				c.Request.URL.Path = "/" + prefix + path
				c.Request.URL.RawPath = ""
				h.Proxy.Handle(c)
				return
			}
		}
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "message": "not found"})
			return
		}
		// 未完成建站时，禁止代理转发，仅提供前端（向导页）
		if !h.IsConfigured() {
			serveSPA(c, dist, fileServer)
			return
		}
		// 显式配置的路径隔离网关优先：命中 /{服务名}/ 段即由它处理（含 disabled → 503）
		if h.Iso != nil && h.Iso.Matches(path) {
			h.Iso.ServeHTTP(c.Writer, c.Request)
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
		if p := stripDeployPrefix(req.URL.Path, req.Host, h); p != req.URL.Path {
			req.URL.Path = p
			req.URL.RawPath = ""
		}
		engine.ServeHTTP(w, req)
	})
}

// serveSPA 提供静态资源或 SPA 回退；多段未知路径返回 404
// prefixFromReferer 从同源 Referer 中找出已启用路由前缀，用于给 SDK 内部
// 根相对请求（/ttwid/check/、/api/user/info 等）补前缀的服务端兜底。
// 只认同源 Referer + 已启用路由，避免跨站借道访问后端。
func prefixFromReferer(c *gin.Context, mgr *proxy.Manager) string {
	ref := c.GetHeader("Referer")
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || !strings.EqualFold(u.Host, c.Request.Host) {
		return ""
	}
	seg := strings.Trim(u.Path, "/")
	if seg == "" {
		return ""
	}
	prefix := strings.Split(seg, "/")[0]
	e, ok := mgr.Get(prefix)
	if ok && e.Route.Status == "active" {
		return prefix
	}
	return ""
}

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
func stripDeployPrefix(p, host string, h *handlers.Handler) string {
	// 子域名模式：Host 已标定后端，路径就是后端根路径，任何一段都不能当部署前缀剥掉
	// （如 app.localhost/users/list 剥成 /list 会直接把后端路由打歪）。
	if h.Proxy.MatchableHost(host) {
		return p
	}
	seg, rest := firstSeg(p)
	if seg == "" || rest == "" || reservedFirstSegments[seg] {
		return p
	}
	if h.Proxy.HasPathPrefix(seg) {
		return p
	}
	// 隔离网关的服务名同样是业务首段：绝不能被当成「部署前缀」剥掉
	if h.Iso != nil && h.Iso.HasPrefix(seg) {
		return p
	}
	// 隔离网关接管路径空间后，路径是「显式声明」的：不再猜测部署前缀，
	// 未声明的多段路径直接 404（NFR-5 不猜测归属）。未声明任何服务时保持原行为。
	if h.Iso != nil && h.Iso.Active() {
		return p
	}
	return rest
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

// optionalAuth 尝试解析 JWT：成功则注入 username（视为管理员），失败也放行。
// 用于「访客与管理员共用、但按身份返回不同字段」的接口（如 GET /routes）。
func optionalAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tokenStr := strings.TrimPrefix(h, "Bearer ")
			if claims, err := auth.ParseToken(secret, tokenStr); err == nil {
				c.Set("username", claims.Username)
				c.Set("role", claims.Role)
			}
		}
		c.Next()
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

// allowedOrigin 判断一个 Origin 是否与请求主机同源。
//
// 旧实现把任意 Origin 原样反射并带上 Allow-Credentials: true —— 任何网站都能
// 用受害浏览器里的 JWT 调本网关的管理 API（CSRF / 会话窃取）。
// 管理控制台与页面同源，同源策略已经够用，不需要跨域白名单。
func allowedOrigin(origin, host string) bool {
	if origin == "" || host == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, host) {
		return true
	}
	// 本机开发常用 localhost / 127.0.0.1 互换，但**端口必须一致** ——
	// 不同端口是不同 origin，放行会让本地任意服务读到管理会话。
	_, originPort, err := net.SplitHostPort(u.Host)
	if err != nil {
		return false
	}
	_, reqPort, err := net.SplitHostPort(host)
	if err != nil || originPort != reqPort {
		return false
	}
	return isLoopbackHost(u.Hostname()) && isLoopbackHost(hostPortOnly(host))
}

func isLoopbackHost(h string) bool {
	h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
	if h == "localhost" || h == "host.docker.internal" || h == "host.containers.internal" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func hostPortOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowedOrigin(origin, c.Request.Host) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
		}
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
