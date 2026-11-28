package main

import (
	"os"
	"reflect"
	"testing"
)

// TestRewriteBarePwdFlag 裸 -pwd 必须改写成带哨兵值，否则 Go flag 包会直接报错退出，
// 交互式改密根本走不到。带值的 -pwd secret / -pwd=secret 必须原样保留。
func TestRewriteBarePwdFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string // os.Args[1:]
		want []string // rewrite 后的 os.Args[1:]
	}{
		{"bare at end", []string{"-pwd"}, []string{"-pwd", interactiveSentinel}},
		{"bare with other flag", []string{"-pwd", "-list"}, []string{"-pwd", interactiveSentinel, "-list"}},
		{"with value", []string{"-pwd", "secret123"}, []string{"-pwd", "secret123"}},
		{"equals form", []string{"-pwd=secret123"}, []string{"-pwd=secret123"}},
		{"not present", []string{"-list"}, []string{"-list"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := os.Args
			defer func() { os.Args = old }()
			os.Args = append([]string{"gwh"}, c.args...)
			rewriteBarePwdFlag(os.Args[1:])
			got := os.Args[1:]
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("hello", 10); got != "hello" {
		t.Errorf("short string unchanged: %q", got)
	}
	if got := trunc("abcdefghij", 5); got != "abcd…" {
		t.Errorf("truncated: %q", got)
	}
	// 中文按 rune 截断，避免截出半个 UTF-8 字符
	if got := trunc("在线服务测试", 4); got != "在线服…" {
		t.Errorf("utf8 trunc: %q", got)
	}
}

func TestCliRan(t *testing.T) {
	if cliRan("", false) {
		t.Error("no flags should not run CLI")
	}
	if !cliRan("secret", false) || !cliRan("", true) {
		t.Error("pwd/list flags should run CLI")
	}
}
