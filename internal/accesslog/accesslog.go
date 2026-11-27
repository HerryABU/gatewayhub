// Package accesslog 访问日志落盘。
//
// 目录结构与记录格式刻意与 NVS（nvs-server/internal/infra.WriteDailyLog +
// middleware.AccessLog）保持一致，让两套系统的日志能用同一套工具检索、同一套
// 分层归档（见 internal/logarchive）压缩：
//
//	log/requests/<YYYY-MM-DD>/<HH>.log     每行一条 JSON（JSONL）
//	log/database/<YYYY-MM-DD>/<HH>.log     预留：数据库日志（同一归档链）
//
// 字段名与 NVS 对齐：timestamp / user_id / method / path / query / ip / ua /
// status / size / latency / headers / body。网关侧另加两个扩展字段
// （route_prefix、kind），NVS 侧没有、遇到时忽略即可。
//
// 写失败只返回错误，调用方决定是否打日志——绝不阻塞转发。
package accesslog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 日志类别（对应 log/<category>/ 一级目录，与 NVS 的 category 同名）
const (
	CategoryRequests = "requests"
	CategoryDatabase = "database"
	// CategoryRewrite 记录改写行为：命中数、完整前缀链、跳过原因。
	// 单列一类是为了让「谁在什么时候被改了什么」可检索，而不是只在 stdout 一闪而过。
	CategoryRewrite = "rewrite"
)

// TimeLayout 是 timestamp 字段的格式，与 NVS 完全一致（本地时间，无时区后缀）。
const TimeLayout = "2006-01-02 15:04:05"

// Entry 一条访问日志记录。
//
// 字段顺序与命名对齐 NVS 的 AccessLog 中间件；user_id 在网关侧没有登录态，
// 恒为 0（保留字段是为了让两边的 JSON 结构可以直接互换消费）。
type Entry struct {
	Timestamp   string            `json:"timestamp"` // 2006-01-02 15:04:05
	UserID      int64             `json:"user_id"`   // 网关恒 0（占位对齐 NVS）
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Query       string            `json:"query"`
	IP          string            `json:"ip"`
	UA          string            `json:"ua"`
	Status      int               `json:"status"`
	Size        int64             `json:"size"`    // 响应字节数
	Latency     string            `json:"latency"` // 形如 1.2ms（与 NVS 的 Duration.String 一致）
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`                   // 请求体片段（≤64KB，见 SniffBody）
	RoutePrefix string            `json:"route_prefix,omitempty"` // 网关扩展：命中的服务前缀
	Kind        string            `json:"kind,omitempty"`         // 网关扩展：path / isolation / subdomain

	// 改写审计字段（仅 rewrite 类别写，requests 类别留空，保持 NVS 结构兼容）
	RewriteHits   int    `json:"rewrite_hits,omitempty"`   // 改写命中数
	RewriteChain  string `json:"rewrite_chain,omitempty"`  // 嵌套链的完整对外前缀
	RewriteSkip   string `json:"rewrite_skip,omitempty"`   // 跳过原因：too_large / unsupported_encoding / decode_failed
	RewriteKind   string `json:"rewrite_kind,omitempty"`   // html / css / js
	RewriteStatus int    `json:"rewrite_status,omitempty"` // 响应状态码
}

// Option 是 FileLogger 的可选项。
type Option func(*FileLogger)

// WithRedact 开启敏感信息脱敏（Cookie / Authorization / token 类字段）。
// 默认关闭，与 NVS 的开发模式（LOG_REDACT_SENSITIVE=false）行为一致。
func WithRedact(on bool) Option { return func(l *FileLogger) { l.redact = on } }

// WithBody 控制是否落盘请求体片段（默认 true）。关掉后 Body 字段恒为空串，
// 但调用方仍在嗅探——想彻底免掉嗅探开销，请在装配处就不要调用 SniffBody。
func WithBody(on bool) Option { return func(l *FileLogger) { l.body = on } }

// FileLogger 按「类别 / 天 / 小时」滚动落盘的访问日志写入器：
//
//	{dir}/requests/2026-11-22/15.log
//
// 线程安全（代理转发并发调用）；文件以 O_APPEND 打开，重启后追加、绝不覆盖。
type FileLogger struct {
	dir    string
	redact bool
	body   bool

	mu      sync.Mutex
	handles map[string]*os.File // key: category/date/hour
}

// New 创建文件日志器（dir 为日志根目录，如 "log"）。
func New(dir string, opts ...Option) *FileLogger {
	l := &FileLogger{dir: dir, body: true, handles: make(map[string]*os.File)}
	for _, o := range opts {
		o(l)
	}
	return l
}

// BodyEnabled 返回是否记录请求体片段（调用方据此决定要不要嗅探）。
func (l *FileLogger) BodyEnabled() bool {
	if l == nil {
		return false
	}
	return l.body
}

// Dir 返回日志根目录。
func (l *FileLogger) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Log 写入一条访问记录到 requests 类别。
func (l *FileLogger) Log(e Entry) error {
	return l.LogCategory(CategoryRequests, e)
}

// LogCategory 写入一条记录到指定类别目录。
func (l *FileLogger) LogCategory(category string, e Entry) error {
	if l == nil || l.dir == "" {
		return nil
	}
	if category == "" {
		category = CategoryRequests
	}
	now := time.Now()
	e.Timestamp = now.Format(TimeLayout)
	if !l.body {
		e.Body = ""
	}
	if l.redact {
		RedactEntry(&e)
	}

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := l.file(category, now)
	if err != nil {
		return err
	}
	_, err = f.Write(line)
	return err
}

// Close 关闭全部文件句柄。
func (l *FileLogger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	for k, f := range l.handles {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(l.handles, k)
	}
	return firstErr
}

// file 返回当前小时对应的文件句柄；跨小时/跨天时关闭其他句柄（避免句柄无限增长）。
func (l *FileLogger) file(category string, now time.Time) (*os.File, error) {
	date := now.Format("2006-01-02")
	hour := now.Format("15")
	key := category + "/" + date + "/" + hour

	if f, ok := l.handles[key]; ok {
		return f, nil
	}
	// 换小时了：除了目标句柄，其余全部关掉（同一进程只会写「当前小时」）
	for k, f := range l.handles {
		_ = f.Close()
		delete(l.handles, k)
	}
	dir := filepath.Join(l.dir, category, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("accesslog mkdir %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, hour+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("accesslog open: %w", err)
	}
	l.handles[key] = f
	return f, nil
}
