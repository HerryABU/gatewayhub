package target

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
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
		return p, fmt.Errorf("target must start with ':' or be a full url: %s", t)
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
