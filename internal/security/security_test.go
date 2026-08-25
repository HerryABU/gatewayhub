package security

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestCtx(method, ct string, body string, contentLength int64) *gin.Context {
	req := httptest.NewRequest(method, "/api/x", strings.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.ContentLength = contentLength
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// TestInspectBodySkipsBodylessMethods 无请求体方法应跳过扫描且不读 body。
func TestInspectBodySkipsBodylessMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodDelete, http.MethodTrace} {
		c := newTestCtx(m, "application/json", `{"a":1}`, -1)
		if got := inspectBody(c, 1024); got != "" {
			t.Errorf("%s 应跳过扫描，got %q", m, got)
		}
	}
}

// TestInspectBodySkipsMultipart multipart 文件上传不应被读取扫描。
func TestInspectBodySkipsMultipart(t *testing.T) {
	c := newTestCtx(http.MethodPost, "multipart/form-data; boundary=x", "BINARYDATA", -1)
	if got := inspectBody(c, 1024); got != "" {
		t.Errorf("multipart 应跳过扫描，got %q", got)
	}
}

// TestInspectBodySmallJSON 小 JSON 请求体应被读取并完整还原。
func TestInspectBodySmallJSON(t *testing.T) {
	c := newTestCtx(http.MethodPost, "application/json", `{"user":"admin"}`, -1)
	got := inspectBody(c, 1024)
	if got != `{"user":"admin"}` {
		t.Errorf("小 JSON 未正确读取，got %q", got)
	}
	restored, _ := io.ReadAll(c.Request.Body)
	if string(restored) != `{"user":"admin"}` {
		t.Errorf("body 未正确还原，got %q", restored)
	}
}

// TestInspectBodyKnownLargeSkips 已知 Content-Length 超限时跳过扫描，body 原样保留。
func TestInspectBodyKnownLargeSkips(t *testing.T) {
	body := strings.Repeat("x", 4096)
	c := newTestCtx(http.MethodPost, "application/json", body, int64(len(body)))
	got := inspectBody(c, 1024)
	if got != "" {
		t.Errorf("超限 body 应跳过扫描，got %q", got)
	}
	restored, _ := io.ReadAll(c.Request.Body)
	if string(restored) != body {
		t.Errorf("超限 body 未原样保留：len=%d want %d", len(restored), len(body))
	}
}

// TestInspectBodyChunkedLargeRestores 未知长度（chunked）超限时跳过扫描，但已读前缀+剩余 body 必须完整还原。
func TestInspectBodyChunkedLargeRestores(t *testing.T) {
	body := strings.Repeat("y", 4096)
	c := newTestCtx(http.MethodPost, "application/json", body, -1)
	got := inspectBody(c, 1024)
	if got != "" {
		t.Errorf("chunked 超限 body 应跳过扫描，got %q", got)
	}
	restored, _ := io.ReadAll(c.Request.Body)
	if string(restored) != body {
		t.Errorf("chunked 超限 body 还原不完整：len=%d want %d", len(restored), len(body))
	}
}
