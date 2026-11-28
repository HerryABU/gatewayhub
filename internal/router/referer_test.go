package router

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestPrefixFromReferer 用同源 Referer 给 SDK 内部根相对请求补前缀。
// 跨站 Referer 必须拒绝，避免借道访问后端。
func TestPrefixFromReferer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 用最小 manager：无路由时任何 Referer 都应返回空（不会 panic）
	mgr := newDummyManager()
	cases := []struct {
		name, ref, host string
	}{
		{"no referer", "", "127.0.0.1:8088"},
		{"cross origin", "http://evil.com/fanq/", "127.0.0.1:8088"},
		{"same origin", "http://127.0.0.1:8088/fanq/", "127.0.0.1:8088"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest("GET", "http://127.0.0.1:8088/ttwid/check/", nil)
		if c.ref != "" {
			ctx.Request.Header.Set("Referer", c.ref)
		}
		got := prefixFromReferer(ctx, mgr)
		if got != "" {
			t.Errorf("%s: 无路由时应返回空，实际 %q", c.name, got)
		}
	}
}
