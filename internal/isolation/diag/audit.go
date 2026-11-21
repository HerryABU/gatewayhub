package diag

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry 是一条配置变更审计记录（FR-4：变更需可审计）。
type Entry struct {
	Time    string `json:"time"`
	Actor   string `json:"actor"`
	Action  string `json:"action"` // reload / set_mode / enable / disable
	Service string `json:"service,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Audit 把变更写成 JSON Lines 追加到文件，同时在内存保留最近若干条供 API 查询。
// 写文件失败不影响主流程（只记日志），审计失败不能拦住请求。
type Audit struct {
	mu   sync.Mutex
	f    *os.File
	path string
	ring []Entry
}

const auditRingSize = 200

// NewAudit 打开（或创建）审计文件；目录不存在时自动创建。
// 文件无法打开时返回一个仅内存模式的 Audit（不阻断启动）。
func NewAudit(path string) *Audit {
	a := &Audit{path: path}
	if path == "" {
		return a
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[isolation][config_error] 审计日志打开失败（仅内存保留）: %v", err)
		return a
	}
	a.f = f
	return a
}

// Path 返回审计文件路径。
func (a *Audit) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Append 追加一条审计记录。
func (a *Audit) Append(e Entry) {
	if a == nil {
		return
	}
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ring = append(a.ring, e)
	if len(a.ring) > auditRingSize {
		a.ring = a.ring[len(a.ring)-auditRingSize:]
	}
	if a.f == nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	if _, err := a.f.Write(append(line, '\n')); err != nil {
		log.Printf("[isolation][config_error] 审计日志写入失败: %v", err)
	}
}

// Recent 返回最近 n 条审计记录（n<=0 表示全部，最多 ring 容量）。
func (a *Audit) Recent(n int) []Entry {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n <= 0 || n > len(a.ring) {
		n = len(a.ring)
	}
	out := make([]Entry, n)
	copy(out, a.ring[len(a.ring)-n:])
	return out
}

// Close 关闭审计文件。
func (a *Audit) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}
