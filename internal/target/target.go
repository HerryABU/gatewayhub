package target

import (
	"fmt"
	"net/url"
	"strings"
)

// Parsed 解析后的后端地址
type Parsed struct {
	Scheme string
	Host   string
	Root   string
}

// Parse 解析后端地址，支持 :8080 / :8080/api/v1 / http://host:port / http://host:port/root / https://...
func Parse(t string) (Parsed, error) {
	var p Parsed
	t = strings.TrimSpace(t)
	if t == "" {
		return p, fmt.Errorf("empty target")
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
