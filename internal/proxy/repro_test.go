package proxy

import (
	"regexp"
	"testing"
)

func TestReproRegex(t *testing.T) {
	re := regexp.MustCompile(`(["'])(/(?:[^"'\\]|\\.)*)(["'])`)
	s := `const routes=[{path:"/home",component:()=>import("/assets/AuthorHome-xyz.js")}];`
	t.Logf("input: %s", s)
	s = re.ReplaceAllStringFunc(s, func(m string) string {
		sub := re.FindStringSubmatch(m)
		t.Logf("match=%q sub=%#v", m, sub)
		return m
	})
	t.Logf("output: %s", s)
}
