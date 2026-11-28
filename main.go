package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/backup"
	"gatewayhub/internal/config"
	"gatewayhub/internal/database"
	"gatewayhub/internal/geo"
	"gatewayhub/internal/handlers"
	"gatewayhub/internal/health"
	isoconfig "gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/diag"
	isoengine "gatewayhub/internal/isolation/engine"
	"gatewayhub/internal/logarchive"
	"gatewayhub/internal/proxy"
	"gatewayhub/internal/router"
	"gatewayhub/internal/security"
	"gatewayhub/internal/stats"
)

//go:embed all:web/dist
var distFS embed.FS

//go:embed data/ip2region.xdb
var ip2regionXDB []byte

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	archiveOnly := flag.Bool("archive", false, "只执行一次日志分层归档后退出（运维手动归档用）")
	pwdValue := flag.String("pwd", "", "强制重置管理员密码：直接给新密码，或裸用 -pwd 交互式输入")
	listOnly := flag.Bool("list", false, "实时探测并列出所有已启用服务的在线状态后退出")
	// Go 的 flag 包对裸 -pwd（string flag 无值）会直接报错退出，
	// 先把裸 -pwd 改写成哨兵值，flag 解析后才认得出来走交互输入。
	rewriteBarePwdFlag(os.Args[1:])
	flag.Parse()

	// 离线运维命令：不启 HTTP 服务、不触发后台任务，执行完直接退出。
	if cliRan(*pwdValue, *listOnly) {
		if err := runCLI(*configPath, *pwdValue, *listOnly); err != nil {
			log.Fatalf("[cli] %v", err)
		}
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// -archive：不启服务，只跑一轮分层归档（适合放进系统计划任务）
	if *archiveOnly {
		opt := logarchive.Options{
			LogDir:     cfg.AccessLog.Dir,
			ArchiveDir: cfg.AccessLog.Archive.Dir,
		}.Resolve()
		outs, err := logarchive.Run(opt)
		for _, o := range outs {
			log.Printf("[logarchive] 产出 %s", o)
		}
		if err != nil {
			log.Fatalf("[logarchive] 归档失败: %v", err)
		}
		log.Printf("[logarchive] 完成：%s → %s，新建 %d 个包", opt.LogDir, opt.ArchiveDir, len(outs))
		return
	}

	db, err := database.Init(&cfg.Database)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}

	// 离线 IP 库已嵌入二进制：目标路径缺失时自动解包（已存在则尊重用户自定义的库）
	if cfg.Geo.Enabled && cfg.Geo.DBPath != "" {
		unpacked, err := geo.EnsureDB(cfg.Geo.DBPath, ip2regionXDB)
		if err != nil {
			log.Printf("IP 库解包失败（可忽略）: %v", err)
		} else if unpacked {
			log.Printf("已从内置资源解包 IP 库到 %s", cfg.Geo.DBPath)
		}
	}
	// 地理位置解析器
	geoResolver := geo.New(cfg.Geo)
	if geoResolver.Loaded() {
		log.Println("离线 IP 库已加载（ip2region）")
	} else {
		log.Println("离线 IP 库未加载，地理位置将显示为未知")
	}

	// 异步日志写入器
	writer := stats.NewWriter(db, geoResolver, 4, cfg.Stats.BatchSize,
		time.Duration(cfg.Stats.BatchInterval)*time.Second, cfg.Stats.SampleRate)
	writer.Start()

	// 反向代理管理器（内存路由表）
	// SelfInfo 让管理器能识别「target 指向网关自身」的自指路由，并限制链式转发跳数。
	proxyMgr := proxy.NewManager(db, writer, cfg.Server.BaseDomain, proxy.SelfInfo{
		Port:    cfg.Server.Port,
		MaxHops: cfg.Server.MaxHops,
	})
	// 访问日志落盘（含请求头，类别/天/小时 组织，与 NVS 同结构），失败不阻塞转发
	var fileLogger *accesslog.FileLogger
	if cfg.AccessLog.Enabled && cfg.AccessLog.Dir != "" {
		fileLogger = accesslog.New(cfg.AccessLog.Dir,
			accesslog.WithBody(cfg.AccessLog.Body),
			accesslog.WithRedact(cfg.AccessLog.RedactSensitive),
		)
		proxyMgr.SetFileLogger(fileLogger)
		log.Printf("访问日志已启用：%s/requests/YYYY-MM-DD/HH.log（请求体=%v，脱敏=%v）",
			cfg.AccessLog.Dir, cfg.AccessLog.Body, cfg.AccessLog.RedactSensitive)
	}
	if err := proxyMgr.Load(); err != nil {
		log.Fatalf("加载路由失败: %v", err)
	}
	log.Printf("已加载 %d 条路由", proxyMgr.Count())

	// 日志分层归档（对齐 NVS：日 7z → 周 tar.gz → 月 tar.gz → 年 tar.xz），启动即跑一次，之后按周期
	if fileLogger != nil && cfg.AccessLog.Archive.Enabled {
		startLogArchive(cfg)
	}

	// 路径隔离网关：显式配置表（isolation.yaml）+ 热加载 + 变更审计
	var isoEngine *isoengine.Engine
	var isoWatcher *isoconfig.Watcher
	if cfg.Isolation.Enabled {
		isoFile := cfg.Isolation.File
		audit := diag.NewAudit(cfg.Isolation.AuditFile)
		defer func() { _ = audit.Close() }()

		table, err := isoconfig.Load(isoFile)
		if err != nil {
			// 配置错误不阻断启动：隔离网关暂不生效，其余功能照常（爆炸半径可控）
			diag.Logf(diag.ConfigError, "", "隔离配置加载失败，隔离网关暂不生效: %v", err)
			table = &isoconfig.Table{Source: isoFile}
		}
		isoEngine = isoengine.New(isoengine.Options{
			Table:        table,
			Audit:        audit,
			Logger:       fileLogger, // 隔离链路与普通反代共用同一个访问日志写入器
			TrustProxies: cfg.Isolation.TrustProxies,
			SelfPort:     cfg.Server.Port, // 自指服务识别（target 指向网关自身）
			MaxHops:      cfg.Server.MaxHops,
		})
		if cfg.Isolation.Watch {
			isoWatcher = isoconfig.NewWatcher(isoFile, table.Hash,
				time.Duration(cfg.Isolation.WatchInterval)*time.Second)
			isoWatcher.Start(func(t *isoconfig.Table, loadErr error) {
				if loadErr != nil {
					diag.Logf(diag.ConfigError, "", "隔离配置热加载失败（继续使用原配置）: %v", loadErr)
					return
				}
				isoEngine.Reload(t, "watcher")
			})
		}
		log.Printf("路径隔离网关已启用：%d 个服务（配置：%s，审计：%s）",
			isoEngine.Count(), isoFile, cfg.Isolation.AuditFile)
	} else {
		log.Println("路径隔离网关未启用（isolation.enabled=false）")
	}

	// 健康检查器
	checker := health.New(db, cfg.HealthCheck)
	checker.Start()

	// 安全防护管理器
	secMgr := security.New(db, cfg.Security)

	// 备份管理器
	backupInterval, _ := time.ParseDuration(cfg.Backup.Interval)
	backupMgr := backup.New(db, cfg.Database.Driver, cfg.Backup.Dir, backupInterval, cfg.Backup.Retain)
	if cfg.Backup.Enabled {
		backupMgr.Start()
	}

	h := &handlers.Handler{
		DB: db, Cfg: cfg, ConfigPath: *configPath,
		Proxy: proxyMgr, Health: checker, Geo: geoResolver, Stats: writer,
		Security: secMgr, Backup: backupMgr, Iso: isoEngine,
	}

	// 已完成后建站向导则补齐演示数据（幂等）
	if h.IsConfigured() {
		if err := database.SeedDemo(db); err != nil {
			log.Printf("演示数据初始化失败（可忽略）: %v", err)
		}
		// 重新加载路由（演示数据可能新增了路由）
		_ = proxyMgr.Load()
	}

	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		log.Fatalf("前端资源嵌入失败: %v", err)
	}
	engine := router.Setup(h, sub)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      engine,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
	}

	go func() {
		if h.IsConfigured() {
			log.Printf("GatewayHub 已启动，监听 http://%s（站点：%s）", addr, h.SiteName())
		} else {
			log.Printf("GatewayHub 已启动，监听 http://%s —— 请先完成建站向导", addr)
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在优雅关闭...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	writer.Stop()
	checker.Stop()
	backupMgr.Stop()
	if fileLogger != nil {
		_ = fileLogger.Close()
	}
	if isoWatcher != nil {
		isoWatcher.Stop()
	}
	log.Println("已退出")
}

// startLogArchive 启动日志分层归档（与 NVS 的 logarchive 同一套层级）：
// 启动即跑一次（把昨天的日志补成包），之后按 access_log.archive.interval 循环。
// 归档失败只告警，绝不影响网关转发。
func startLogArchive(cfg *config.Config) {
	opt := logarchive.Options{
		LogDir:     cfg.AccessLog.Dir,
		ArchiveDir: cfg.AccessLog.Archive.Dir,
	}.Resolve()
	interval := 24 * time.Hour
	if cfg.AccessLog.Archive.Interval != "" {
		if d, err := time.ParseDuration(cfg.AccessLog.Archive.Interval); err == nil && d > 0 {
			interval = d
		} else {
			log.Printf("[logarchive] interval=%q 无法解析，回退 24h", cfg.AccessLog.Archive.Interval)
		}
	}
	log.Printf("[logarchive] 分层归档已启用：%s → %s（周期 %s；7z=%v）",
		opt.LogDir, opt.ArchiveDir, interval, logarchive.SevenZipPath() != "")

	run := func(tag string) {
		outs, err := logarchive.Run(opt)
		switch {
		case err != nil:
			log.Printf("[logarchive] %s归档有告警（已生成 %d 个包）：%v", tag, len(outs), err)
		case len(outs) == 0:
			log.Printf("[logarchive] %s归档：无新内容", tag)
		default:
			sample := outs
			if len(sample) > 3 {
				sample = sample[:3]
			}
			log.Printf("[logarchive] %s归档完成：新建 %d 个包（%s 等）",
				tag, len(outs), strings.Join(sample, ", "))
		}
	}

	go run("启动")
	go func() {
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for range tk.C {
			run("定时")
		}
	}()
}
