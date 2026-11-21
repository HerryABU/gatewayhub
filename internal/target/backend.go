package target

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ParseBackend 解析「后端地址」，比 Parse 更宽松，供显式配置表使用。
//
// 支持形态（FR-2：后端地址为 IP:端口 或 端口）：
//
//	8080                  → http://127.0.0.1:8080
//	:8080                 → http://127.0.0.1:8080
//	10.0.0.5:8080         → http://10.0.0.5:8080
//	10.0.0.5:8080/root    → http://10.0.0.5:8080 + 根路径 /root
//	http://host:port/root → 原样解析（支持 https）
//
// 与 Parse 的差别：接受裸 host:port，且拒绝「没有端口的裸主机名」——
// 显式配置不允许靠默认端口猜，猜错会把流量打到错误端口。
func ParseBackend(t string) (Parsed, error) {
	var p Parsed
	s := strings.TrimSpace(t)
	if s == "" {
		return p, fmt.Errorf("empty target")
	}
	if strings.Contains(s, "${") {
		expanded, err := ExpandEnv(s)
		if err != nil {
			return p, err
		}
		s = strings.TrimSpace(expanded)
	}
	// 完整 URL 或 :port 形态复用既有解析器，保持行为一致
	if strings.Contains(s, "://") || strings.HasPrefix(s, ":") {
		return Parse(s)
	}
	// 纯数字：视为端口
	if allDigits(s) {
		return Parsed{Scheme: "http", Host: "127.0.0.1:" + s}, nil
	}

	host, root := s, ""
	if i := strings.Index(s, "/"); i >= 0 {
		host, root = s[:i], s[i:]
	}
	if host == "" {
		return p, fmt.Errorf("invalid backend: %s", t)
	}
	if _, port, err := net.SplitHostPort(host); err != nil {
		return p, fmt.Errorf("backend %q 缺少合法端口（形如 10.0.0.5:8080 或 :8080）", t)
	} else if n, convErr := strconv.Atoi(port); convErr != nil || n <= 0 || n > 65535 {
		return p, fmt.Errorf("backend %q 端口非法: %s", t, port)
	}
	return Parsed{Scheme: "http", Host: host, Root: strings.TrimSuffix(root, "/")}, nil
}

// allDigits 判断字符串是否全为数字（且非空）。
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
