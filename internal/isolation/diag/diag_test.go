package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "isolation-audit.log")
	a := NewAudit(path)
	defer a.Close()
	a.Append(Entry{Actor: "admin", Action: "set_mode", Service: "A", From: "strip", To: "isolated"})
	a.Append(Entry{Actor: "system", Action: "reload", Detail: "hash=abc"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("审计文件未生成: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("应写入 2 行 JSONL，得到 %d", len(lines))
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("审计行不是合法 JSON: %v", err)
	}
	if e.Action != "set_mode" || e.Service != "A" || e.From != "strip" || e.To != "isolated" {
		t.Fatalf("审计内容异常: %+v", e)
	}
	if e.Time == "" {
		t.Fatal("应自动补时间")
	}
	recent := a.Recent(1)
	if len(recent) != 1 || recent[0].Action != "reload" {
		t.Fatalf("Recent(1) 异常: %+v", recent)
	}
}

func TestAuditRingCapAndNilSafety(t *testing.T) {
	var nilAudit *Audit
	nilAudit.Append(Entry{Action: "x"})
	if nilAudit.Recent(3) != nil || nilAudit.Close() != nil || nilAudit.Path() != "" {
		t.Fatal("nil 审计器应安全")
	}

	a := NewAudit("") // 仅内存模式
	for i := 0; i < auditRingSize+10; i++ {
		a.Append(Entry{Action: "reload"})
	}
	if got := len(a.Recent(0)); got != auditRingSize {
		t.Fatalf("内存环容量 = %d, want %d", got, auditRingSize)
	}
	if got := len(a.Recent(5)); got != 5 {
		t.Fatalf("Recent(5) = %d", got)
	}
}
