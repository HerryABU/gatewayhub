package main

// 命令行离线运维开关（不启动 HTTP 服务、不触发后台任务）：
//
//   gatewayhub.exe -pwd            交互式强制重置管理员密码（提示两次输入）
//   gatewayhub.exe -pwd 新密码       直接强制重置管理员密码（无需旧密码）
//   gatewayhub.exe -list           实时探测并列出所有已启用路由的在线状态
//
// 这些命令与 -archive 一样：加载配置/数据库 → 执行 → 退出。
// 强制改密用于「忘记密码 / 被锁在门外」时从本机恢复访问，因此刻意不需要旧密码；
// 调用者必须能读到本机 config.yaml 与数据库——这本身就是本机管理权限的证明。

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gatewayhub/internal/auth"
	"gatewayhub/internal/config"
	"gatewayhub/internal/database"
	"gatewayhub/internal/models"
	"gatewayhub/internal/target"

	"gorm.io/gorm"
)

// interactiveSentinel 是 main 对裸 -pwd 的改写值（见 main.go），
// 表示「走交互式输入」而非把它当字面密码。
const interactiveSentinel = "__GWH_INTERACTIVE_PWD__"

// rewriteBarePwdFlag 把裸 -pwd / --pwd（后面没有值）改写成 -pwd <哨兵值>。
// 必须在 flag.Parse() 之前调用：Go 的 flag 包遇到 string flag 缺值会直接报错退出。
// 只认「孤立的 -pwd」——若形如 -pwd secret 或 -pwd=secret 则保持原样。
func rewriteBarePwdFlag(args []string) {
	out := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		a := args[i]
		out = append(out, a)
		if a != "-pwd" && a != "--pwd" {
			continue
		}
		// 下一项存在且不是另一个 flag → 视为密码值，原样保留
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			continue
		}
		// 裸 -pwd：插入哨兵占位
		out = append(out, interactiveSentinel)
	}
	os.Args = append([]string{os.Args[0]}, out...)
}

// cliRan 报告本次进程是否执行了离线命令。
func cliRan(pwdValue string, doList bool) bool {
	return pwdValue != "" || doList
}

// runCLI 执行离线命令；调用方执行后应直接 return。
func runCLI(configPath, pwdValue string, doList bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	db, err := database.Init(&cfg.Database)
	if err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}

	if pwdValue != "" {
		pwd := pwdValue
		if pwd == interactiveSentinel {
			pwd, err = readPasswordInteractive()
			if err != nil {
				return err
			}
		}
		if err := resetAdminPassword(db, pwd); err != nil {
			return err
		}
	}
	if doList {
		if err := listOnlineServices(db, cfg); err != nil {
			return err
		}
	}
	return nil
}

// resetAdminPassword 强制重置第一条 admin 用户的密码（无需旧密码）。
func resetAdminPassword(db *gorm.DB, plain string) error {
	if len(plain) < 6 {
		return fmt.Errorf("新密码长度至少 6 位，密码未修改")
	}
	var user models.User
	if err := db.Where("role = ?", "admin").Order("id asc").First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("未找到管理员账号；请先通过建站向导创建")
		}
		return fmt.Errorf("查询管理员失败: %w", err)
	}
	hash, err := auth.HashPassword(plain)
	if err != nil {
		return fmt.Errorf("密码加密失败: %w", err)
	}
	if err := db.Model(&user).Update("password", hash).Error; err != nil {
		return fmt.Errorf("写入新密码失败: %w", err)
	}
	fmt.Printf("管理员 %q 密码已强制重置（角色=%s）\n", user.Username, user.Role)
	return nil
}

// readPasswordInteractive 从 stdin 读两次密码并确认一致。
func readPasswordInteractive() (string, error) {
	r := bufio.NewReader(os.Stdin)
	fmt.Print("新密码（至少 6 位）: ")
	first, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}
	first = strings.TrimSpace(first)
	fmt.Print("再输一次确认: ")
	second, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("读取确认密码失败: %w", err)
	}
	second = strings.TrimSpace(second)
	if first != second {
		return "", fmt.Errorf("两次输入不一致，密码未修改")
	}
	return first, nil
}

// listOnlineServices 实时探测并打印路由状态。
// 并发探测、整体 30 秒超时；单条失败不影响其它条目。
func listOnlineServices(db *gorm.DB, cfg *config.Config) error {
	var routes []models.Route
	if err := db.Where("status = ?", models.StatusActive).Order("id asc").Find(&routes).Error; err != nil {
		return fmt.Errorf("查询路由失败: %w", err)
	}
	if len(routes) == 0 {
		fmt.Println("没有已启用的服务")
		return nil
	}

	timeout := time.Duration(cfg.HealthCheck.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	endpoint := cfg.HealthCheck.HealthEndpoint
	if endpoint == "" {
		endpoint = "/"
	}

	type result struct {
		prefix  string
		name    string
		target  string
		online  bool
		latency int64
	}
	results := make([]result, len(routes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 12) // 并发上限，避免一次性打爆后端
	for i, r := range routes {
		wg.Add(1)
		go func(i int, r models.Route) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			on, ms := probeOnline(r.Target, endpoint, timeout)
			results[i] = result{prefix: r.Prefix, name: r.Name, target: r.Target, online: on, latency: ms}
		}(i, r)
	}
	// 整体超时兜底：极端情况下探测卡住也不让 CLI 永远挂起
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		fmt.Println("警告：部分探测超时，以下为已完成结果")
	}

	fmt.Printf("%-18s %-28s %-8s %-10s %s\n", "转发名", "服务", "状态", "延迟", "后端")
	fmt.Println(strings.Repeat("-", 92))
	onlineCount := 0
	for _, r := range results {
		state := "离线"
		if r.online {
			state = "在线"
			onlineCount++
		}
		ms := "-"
		if r.online {
			ms = fmt.Sprintf("%dms", r.latency)
		}
		fmt.Printf("%-18s %-28s %-8s %-10s %s\n", trunc(r.prefix, 18), trunc(r.name, 28), state, ms, trunc(r.target, 40))
	}
	fmt.Printf("\n共 %d 个已启用服务，%d 在线，%d 离线\n", len(routes), onlineCount, len(routes)-onlineCount)
	return nil
}

// probeOnline 探测单个后端：先 HTTP(S) GET，失败再 TCP 拨号兜底。
func probeOnline(targetURL, endpoint string, timeout time.Duration) (bool, int64) {
	p, err := target.Parse(targetURL)
	if err != nil || p.Host == "" {
		return false, 0
	}
	url := p.Scheme + "://" + p.Host + endpoint
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err == nil {
		start := time.Now()
		client := &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			return true, time.Since(start).Milliseconds()
		}
	}
	// TCP 兜底
	start := time.Now()
	conn, err := net.DialTimeout("tcp", p.Host, timeout)
	if err == nil {
		conn.Close()
		return true, time.Since(start).Milliseconds()
	}
	return false, 0
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
