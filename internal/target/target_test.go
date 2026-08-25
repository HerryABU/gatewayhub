package target

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in     string
		scheme string
		host   string
		root   string
		ok     bool
	}{
		{":8080", "http", "127.0.0.1:8080", "", true},
		{":8080/api/v1", "http", "127.0.0.1:8080", "/api/v1", true},
		{"http://example.com:8443", "http", "example.com:8443", "", true},
		{"https://example.com", "https", "example.com", "", true},
		{"https://example.com/api/v1/", "https", "example.com", "/api/v1", true},
		{"http://192.168.1.10:9090/root", "http", "192.168.1.10:9090", "/root", true},
		{"", "", "", "", false},
		{"not-a-url", "", "", "", false},
		{"ftp://x.com", "", "", "", false},
	}
	for _, c := range cases {
		p, err := Parse(c.in)
		if c.ok && err != nil {
			t.Errorf("Parse(%q) 意外错误: %v", c.in, err)
			continue
		}
		if !c.ok {
			if err == nil {
				t.Errorf("Parse(%q) 应失败", c.in)
			}
			continue
		}
		if p.Scheme != c.scheme || p.Host != c.host || p.Root != c.root {
			t.Errorf("Parse(%q) = %+v, want %s|%s|%s", c.in, p, c.scheme, c.host, c.root)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("GW_TEST_HOST", "order.internal:8443")
	t.Setenv("GW_TEST_PORT", "8080")

	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"http://${GW_TEST_HOST}/api", "http://order.internal:8443/api", false},
		{":${GW_TEST_PORT}", ":8080", false},
		{"http://${GW_TEST_HOST}", "http://order.internal:8443", false},
		{"http://${GW_MISSING_VAR}:80", "", true}, // 未设置报错
		{"no vars here", "no vars here", false},
	}
	for _, c := range cases {
		got, err := ExpandEnv(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ExpandEnv(%q) 应报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ExpandEnv(%q) 意外错误: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ExpandEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseWithEnv(t *testing.T) {
	t.Setenv("GW_TEST_HOST", "example.com")
	t.Setenv("GW_TEST_PORT", "9443")
	p, err := Parse("https://${GW_TEST_HOST}:${GW_TEST_PORT}/api/v1")
	if err != nil {
		t.Fatalf("带 env 的 Parse 失败: %v", err)
	}
	if p.Scheme != "https" || p.Host != "example.com:9443" || p.Root != "/api/v1" {
		t.Errorf("带 env 的 Parse 结果错误: %+v", p)
	}
	if _, err := Parse("http://${GW_NOT_SET_VAR}:80"); err == nil {
		t.Error("env 未设置时应解析失败")
	}
}
