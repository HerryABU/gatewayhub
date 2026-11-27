package router

import "testing"

func TestAllowedOrigin(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"http://localhost:8088", "localhost:8088", true},
		{"http://127.0.0.1:8088", "localhost:8088", true}, // 本机开发两种写法互通
		{"https://localhost:8443", "localhost:8443", true},
		{"https://evil.example", "localhost:8088", false},  // 不能反射任意 Origin
		{"http://localhost:9999", "localhost:8088", false}, // 端口不同不算同源
		{"javascript:alert(1)", "localhost:8088", false},
		{"", "localhost:8088", false},
		{"http://localhost:8088", "", false},
	}
	for _, c := range cases {
		if got := allowedOrigin(c.origin, c.host); got != c.want {
			t.Errorf("allowedOrigin(%q,%q)=%v want %v", c.origin, c.host, got, c.want)
		}
	}
}
