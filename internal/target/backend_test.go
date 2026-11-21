package target

import "testing"

func TestParseBackend(t *testing.T) {
	cases := []struct {
		in     string
		scheme string
		host   string
		root   string
		ok     bool
	}{
		{"9001", "http", "127.0.0.1:9001", "", true},
		{":8080", "http", "127.0.0.1:8080", "", true},
		{"127.0.0.1:9001", "http", "127.0.0.1:9001", "", true},
		{"10.0.0.5:8080/root", "http", "10.0.0.5:8080", "/root", true},
		{"http://example.com:8443", "http", "example.com:8443", "", true},
		{"https://example.com/api/v1/", "https", "example.com", "/api/v1", true},
		{"[::1]:9000", "http", "[::1]:9000", "", true},
		{"", "", "", "", false},
		{"example.com", "", "", "", false},  // 缺端口，禁止猜
		{"10.0.0.5:abc", "", "", "", false}, // 端口非数字
		{"not/a/url", "", "", "", false},    // 非 host:port
	}
	for _, c := range cases {
		p, err := ParseBackend(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParseBackend(%q) 意外错误: %v", c.in, err)
				continue
			}
			if p.Scheme != c.scheme || p.Host != c.host || p.Root != c.root {
				t.Errorf("ParseBackend(%q) = %+v, want %s|%s|%s", c.in, p, c.scheme, c.host, c.root)
			}
			continue
		}
		if err == nil {
			t.Errorf("ParseBackend(%q) 应失败，得到 %+v", c.in, p)
		}
	}
}
