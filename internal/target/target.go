package target

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Parsed 解析后的后端地址
type Parsed struct {
	Scheme string
	Host   string
	Root   string
}

// envRe 匹配 ${VAR} 形式的环境变量引用
var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv 展开字符串中的 ${VAR} 环境变量；未设置的变量报错（避免拼出错误地址）
func ExpandEnv(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var err error
	out := envRe.ReplaceAllStringFunc(s, func(m string) string {
		name := envRe.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			if err == nil {
				err = fmt.Errorf("environment variable %s is not set", name)
			}
			return m
		}
		return v
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// Parse 解析后端地址，支持 :8080 / :8080/api/v1 / http://host:port / http://host:port/root / https://...
// 目标地址支持 ${VAR} 环境变量引用（如 http://${ORDER_HOST}:8080），未设置时解析失败。
func Parse(t string) (Parsed, error) {
	var p Parsed
	t = strings.TrimSpace(t)
	if t == "" {
		return p, fmt.Errorf("empty target")
	}
	if strings.Contains(t, "${") {
		expanded, err := ExpandEnv(t)
		if err != nil {
			return p, err
		}
		t = strings.TrimSpace(expanded)
	}
	if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
		u, err := url.Parse(t)
		if err != nil {
			return p, fmt.Errorf("invalid url target: %s", t)
		}
		if u.Host == "" {
			return p, fmt.Errorf("invalid url target: %s", t)
		}
		p.Scheme = u.Scheme
		p.Host = u.Host
		p.Root = strings.TrimSuffix(u.Path, "/")
		return p, nil
	}
	if !strings.HasPrefix(t, ":") {
		// 常见误写：host:port（如 127.0.0.1:9100）。这里给出可直接照抄的两种正解，
		// 否则用户只能面对一句 "must start with ':'" 猜格式。
		return p, fmt.Errorf("target %q 不合法：只接受 `:端口`（如 :9100）、`:端口/根路径`，"+
			"或完整 URL（如 http://127.0.0.1:9100）；不要写成 host:port", t)
	}
	rest := t[1:] // 形如 8080 或 8080/api/v1
	idx := strings.Index(rest, "/")
	if idx >= 0 {
		p.Host = "127.0.0.1:" + rest[:idx]
		p.Root = strings.TrimSuffix(rest[idx:], "/")
	} else {
		p.Host = "127.0.0.1:" + rest
	}
	p.Scheme = "http"
	return p, nil
}

// selfHosts 是对「指向本机」的主机名判定集合。
// 只列真正会把请求送回本进程的写法，避免把同名的远端机器误判成自己。
var selfHosts = map[string]bool{
	"127.0.0.1":                true,
	"localhost":                true,
	"::1":                      true,
	"0.0.0.0":                  true,
	"host.docker.internal":     true,
	"gateway.docker.internal":  true,
	"host.containers.internal": true,
}

// IsSelf 判断后端地址是否指向网关自身（自指）。
//
// 这是**尽力而为**的识别，只用于打日志与提前告警，绝不用来决定「能不能转发」：
// 判断错了顶多日志不准，判断漏了还有 hopguard 兜底（真自环会在跳数上限处 508），
// 所以不需要覆盖「公网域名绕回来」这类无法从地址看出来的情况。
//
// selfPort 是网关自己的监听端口；baseDomain 非空时同域名的根也算自指
// （如 base_domain=localhost 时 target=http://localhost:8088）。
func IsSelf(p Parsed, selfPort int, baseDomain string) bool {
	if selfPort <= 0 {
		return false
	}
	host, port := splitHostPort(p)
	if port != selfPort {
		return false
	}
	h := strings.ToLower(host)
	if selfHosts[h] {
		return true
	}
	if bd := strings.ToLower(strings.TrimSpace(baseDomain)); bd != "" && h == bd {
		return true
	}
	return false
}

// splitHostPort 拆分 Parsed.Host；缺端口时按 scheme 补默认端口。
func splitHostPort(p Parsed) (string, int) {
	host, portStr, err := net.SplitHostPort(p.Host)
	if err != nil || portStr == "" {
		// 没有端口（如 http://localhost）：按 scheme 取默认值
		host = p.Host
		if p.Scheme == "https" {
			return strings.Trim(host, "[]"), 443
		}
		return strings.Trim(host, "[]"), 80
	}
	port, convErr := strconv.Atoi(portStr)
	if convErr != nil {
		return host, 0
	}
	return host, port
}

// JoinPath 拼接根路径与请求路径
func JoinPath(root, rest string) string {
	root = strings.TrimSuffix(root, "/")
	if root == "" {
		if rest == "" || rest == "/" {
			return "/"
		}
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return rest
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return root + rest
}
