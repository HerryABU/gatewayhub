package accesslog

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogLayoutAndFields 校验落盘路径与字段名都与 NVS 一致：
// log/requests/<YYYY-MM-DD>/<HH>.log，JSONL，字段 timestamp/user_id/.../body。
func TestLogLayoutAndFields(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	defer l.Close()

	if err := l.Log(Entry{
		UserID:      0,
		Method:      "GET",
		Path:        "/openlist/",
		Query:       "a=1",
		IP:          "127.0.0.1:5000",
		UA:          "Mozilla/5.0",
		Status:      200,
		Size:        11911,
		Latency:     "12.3ms",
		Headers:     map[string]string{"Accept": "*/*"},
		Body:        "",
		RoutePrefix: "openlist",
		Kind:        "isolation",
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	now := time.Now()
	want := filepath.Join(dir, "requests", now.Format("2006-01-02"), now.Format("15")+".log")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("未写到 NVS 同构路径 %s: %v", want, err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("每条记录必须以换行结尾（JSONL）: %q", data)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &got); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	for _, k := range []string{"timestamp", "user_id", "method", "path", "query", "ip",
		"ua", "status", "size", "latency", "headers", "body", "route_prefix", "kind"} {
		if _, ok := got[k]; !ok {
			t.Errorf("缺字段 %q（需与 NVS 记录对齐）: %v", k, got)
		}
	}
	// timestamp 必须是 2006-01-02 15:04:05（与 NVS 完全一致，不是 RFC3339）
	ts, _ := got["timestamp"].(string)
	if _, err := time.Parse(TimeLayout, ts); err != nil {
		t.Errorf("timestamp 格式应为 %q，实际 %q", TimeLayout, ts)
	}
}

// TestBodySwitchOff 关闭 body 选项后，即使调用方传了片段也不落盘。
func TestBodySwitchOff(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, WithBody(false))
	defer l.Close()
	if l.BodyEnabled() {
		t.Fatal("WithBody(false) 未生效")
	}
	if err := l.Log(Entry{Method: "POST", Path: "/x", Body: `{"a":1}`}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := filepath.Join(dir, "requests", now.Format("2006-01-02"), now.Format("15")+".log")
	data, _ := os.ReadFile(p)
	var got Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &got); err != nil {
		t.Fatal(err)
	}
	if got.Body != "" {
		t.Fatalf("body 应被丢弃，实际 %q", got.Body)
	}
	if l.BodyEnabled() {
		t.Fatal("BodyEnabled 应为 false")
	}
}

// TestRedact 校验脱敏：Cookie/Authorization 整条替换，query 与 JSON body 的值替换。
func TestRedact(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, WithRedact(true))
	defer l.Close()

	err := l.Log(Entry{
		Method: "POST",
		Path:   "/go-nvs/api/auth/login",
		Query:  "next=/home&token=secret-token",
		Headers: map[string]string{
			"Cookie":        "token=eyJhbGciOi",
			"Authorization": "Bearer abc.def.ghi",
			"Accept":        "*/*",
		},
		Body: `{"username":"u","password":"p@ss","nested":{"refresh_token":"r3fr3sh"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	p := filepath.Join(dir, "requests", now.Format("2006-01-02"), now.Format("15")+".log")
	data, _ := os.ReadFile(p)
	line := strings.TrimSpace(string(data))
	var got Entry
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.Headers["Cookie"] != redacted || got.Headers["Authorization"] != redacted {
		t.Errorf("Cookie/Authorization 未脱敏: %v", got.Headers)
	}
	if got.Headers["Accept"] != "*/*" {
		t.Errorf("无关请求头被误改: %q", got.Headers["Accept"])
	}
	if strings.Contains(got.Query, "secret-token") {
		t.Errorf("query 里的 token 未脱敏: %s", got.Query)
	}
	if !strings.Contains(got.Query, "next=%2Fhome") && !strings.Contains(got.Query, "next=/home") {
		t.Errorf("无关 query 参数被误改: %s", got.Query)
	}
	if strings.Contains(got.Body, "p@ss") || strings.Contains(got.Body, "r3fr3sh") {
		t.Errorf("body 里的口令/令牌未脱敏: %s", got.Body)
	}
	if !strings.Contains(got.Body, `"password":"`+redacted+`"`) {
		t.Errorf("body 脱敏后应保留键名: %s", got.Body)
	}
}

// TestSniffBodyRestoresStream 校验嗅探之后请求体仍能完整读回（不能丢字节）。
func TestSniffBodyRestoresStream(t *testing.T) {
	payload := strings.Repeat("x", 100*1024) // 超过 64KB 嗅探上限
	req, err := http.NewRequest("POST", "http://gw/x", io.NopCloser(strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	snip := SniffBody(req)
	if len(snip) != maxBodySniff {
		t.Fatalf("嗅探长度应为 %d，实际 %d", maxBodySniff, len(snip))
	}
	rest, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	// 还原后的读流必须给出**完整原文**（含已嗅探的那部分）——反向代理正是靠它转发正文
	if len(rest) != len(payload) {
		t.Fatalf("还原后长度不符：want %d got %d", len(payload), len(rest))
	}
	if string(rest) != payload {
		t.Fatal("还原内容与原始正文不一致（嗅探丢了字节或重复了字节）")
	}
	if !strings.HasPrefix(string(rest), snip) {
		t.Fatal("还原后的前段应与嗅探结果一致")
	}
}

// TestSniffBodySkipsMultipart 校验 multipart 完全不读取（读了再还原会截断上传）。
func TestSniffBodySkipsMultipart(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://gw/up", io.NopCloser(strings.NewReader("--BOUNDARY--")))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=BOUNDARY")

	if got := SniffBody(req); got != placeholderMultipart {
		t.Fatalf("multipart 应返回占位符，实际 %q", got)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "--BOUNDARY--" {
		t.Fatalf("multipart 正文被消费了：%q", body)
	}
}

// TestSniffBodyBinary 校验二进制正文只记类型与长度。
func TestSniffBodyBinary(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://gw/up", io.NopCloser(strings.NewReader("\x89PNG...")))
	req.Header.Set("Content-Type", "image/png")
	req.ContentLength = 7
	if got := SniffBody(req); !strings.HasPrefix(got, placeholderBinary) {
		t.Fatalf("二进制正文应记占位符，实际 %q", got)
	}
}

// TestCategorySeparateDirs 校验不同类别落到不同一级目录（requests / database）。
func TestCategorySeparateDirs(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	defer l.Close()
	if err := l.Log(Entry{Path: "/a"}); err != nil {
		t.Fatal(err)
	}
	if err := l.LogCategory(CategoryDatabase, Entry{Path: "/db"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, c := range []string{CategoryRequests, CategoryDatabase} {
		p := filepath.Join(dir, c, now.Format("2006-01-02"))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("类别目录 %s 未创建: %v", p, err)
		}
	}
}
