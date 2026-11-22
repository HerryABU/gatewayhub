package handlers

import "testing"

func TestParseFriendLinksDefaults(t *testing.T) {
	for _, raw := range []string{"", "  ", "{bad json", "null"} {
		links, ok := parseFriendLinks(raw)
		if ok {
			t.Errorf("raw=%q 应视为未设置，实际 ok=true", raw)
		}
		if len(links) != 0 {
			t.Errorf("raw=%q 解析阶段不应返回默认列表（由 friendLinks 兜底），got %v", raw, links)
		}
	}
	if got := DefaultFriendLinks(); len(got) != 1 || got[0].URL != "https://github.com/HerryABU/gatewayhub" {
		t.Fatalf("默认 GWH 友链不对: %+v", got)
	}
}

func TestParseFriendLinksEmptyListIsDeliberate(t *testing.T) {
	links, ok := parseFriendLinks("[]")
	if !ok || len(links) != 0 {
		t.Fatalf("显式空列表不能被默认值覆盖: %v,%v", links, ok)
	}
}

func TestParseFriendLinksFiltersUnsafeAndFillsID(t *testing.T) {
	raw := `[
		{"name":"GitHub","url":"https://github.com/x","description":" repo "},
		{"name":"bad","url":"javascript:alert(1)"},
		{"name":"data","url":"data:text/html,<script>1</script>"},
		{"name":"relative","url":"/local/path"}
	]`
	links, ok := parseFriendLinks(raw)
	if !ok || len(links) != 1 {
		t.Fatalf("只应保留 1 条安全链接，got %d,%v", len(links), ok)
	}
	if links[0].ID != "link-0" || links[0].Description != "repo" {
		t.Fatalf("ID/描述处理错误: %+v", links[0])
	}
}

func TestValidateFriendLink(t *testing.T) {
	cases := []struct {
		name string
		link FriendLink
		bad  bool
	}{
		{"valid", FriendLink{Name: "Example", URL: "https://example.com/path?q=1"}, false},
		{"http", FriendLink{Name: "本地", URL: "http://127.0.0.1:8080/"}, false},
		{"empty name", FriendLink{Name: " ", URL: "https://example.com"}, true},
		{"no scheme", FriendLink{Name: "x", URL: "example.com"}, true},
		{"javascript", FriendLink{Name: "x", URL: "javascript:alert(1)"}, true},
		{"data", FriendLink{Name: "x", URL: "data:text/html,x"}, true},
		{"long desc", FriendLink{Name: "x", URL: "https://x.com", Description: string(make([]rune, 121))}, true},
	}
	for _, c := range cases {
		got := validateFriendLink(c.link)
		if (got != "") != c.bad {
			t.Errorf("%s: validate=%q bad=%v", c.name, got, c.bad)
		}
	}
}
