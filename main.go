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
	"syscall"
	"time"

	"gatewayhub/internal/accesslog"
	"gatewayhub/internal/backup"
	"gatewayhub/internal/config"
	"gatewayhub/internal/database"
	"gatewayhub/internal/geo"
	"gatewayhub/internal/handlers"
	"gatewayhub/internal/health"
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
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
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
	proxyMgr := proxy.NewManager(db, writer, cfg.Server.BaseDomain)
	// 访问日志落盘（含请求头，按 天/小时 组织），失败不阻塞转发
	var fileLogger *accesslog.FileLogger
	if cfg.AccessLog.Enabled && cfg.AccessLog.Dir != "" {
		fileLogger = accesslog.New(cfg.AccessLog.Dir)
		proxyMgr.SetFileLogger(fileLogger)
		log.Printf("访问日志已启用：%s/YYYY-MM-DD/HH.log", cfg.AccessLog.Dir)
	}
	if err := proxyMgr.Load(); err != nil {
		log.Fatalf("加载路由失败: %v", err)
	}
	log.Printf("已加载 %d 条路由", proxyMgr.Count())

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
		Security: secMgr, Backup: backupMgr,
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
	log.Println("已退出")
}
