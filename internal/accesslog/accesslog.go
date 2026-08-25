package accesslog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry 一条访问日志记录（含完整请求头）
type Entry struct {
	Time        string            `json:"time"`         // RFC3339 本地时间
	Method      string            `json:"method"`       // 请求方法
	Path        string            `json:"path"`         // 请求路径（含查询串）
	Status      int               `json:"status"`       // 响应状态码
	LatencyMs   int64             `json:"latency_ms"`   // 处理耗时（毫秒）
	ClientIP    string            `json:"client_ip"`    // 客户端 IP
	UserAgent   string            `json:"user_agent"`   // UA
	RoutePrefix string            `json:"route_prefix"` // 命中的路由前缀（空=网关自身）
	Headers     map[string]string `json:"headers"`      // 完整请求头（Host/Referer/X-Forwarded-* 等）
}

// FileLogger 按天/小时滚动落盘的访问日志写入器：
//
//	logs/2026-08-25/17.log        （一天一个文件夹，一个小时内一个文件，每行一条 JSON）
//
// 线程安全（代理转发并发调用）；写失败仅记录 stderr，绝不阻塞转发。
type FileLogger struct {
	dir string
	mu  sync.Mutex

	curDate string // YYYY-MM-DD
	curHour string // HH
	file    *os.File
}

// New 创建文件日志器（dir 为日志根目录，如 "logs"）
func New(dir string) *FileLogger {
	return &FileLogger{dir: dir}
}

// Log 写入一条访问记录（自动按时间滚动文件）
func (l *FileLogger) Log(e Entry) error {
	if l == nil {
		return nil
	}
	now := time.Now()
	e.Time = now.Format(time.RFC3339)
	date := now.Format("2006-01-02")
	hour := now.Format("15")

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.roll(date, hour); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = l.file.Write(line)
	return err
}

// Close 关闭当前文件句柄
func (l *FileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

// roll 切换到正确的 天/小时 文件（日期或小时变化时关闭旧文件、创建新文件）
func (l *FileLogger) roll(date, hour string) error {
	if l.file != nil && l.curDate == date && l.curHour == hour {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	dir := filepath.Join(l.dir, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("accesslog mkdir %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, hour+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("accesslog open: %w", err)
	}
	l.file = f
	l.curDate = date
	l.curHour = hour
	return nil
}
