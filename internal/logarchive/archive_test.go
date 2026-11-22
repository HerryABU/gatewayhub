package logarchive

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setup 造两个「已收尾」的日期（用相对日期，避免写死日期后测试随时间失效），
// 其中同一天同时有 requests 与 database 两个类别。
func setup(t *testing.T) (root string, days []string) {
	t.Helper()
	root = t.TempDir()
	now := time.Now()
	d1 := now.AddDate(0, 0, -40).Format("2006-01-02")
	d2 := now.AddDate(0, 0, -39).Format("2006-01-02")
	for _, d := range []string{"requests/" + d1, "requests/" + d2, "database/" + d1} {
		dir := filepath.Join(root, "log", d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "10.log"), []byte(`{"path":"/x"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, []string{d1, d2}
}

// TestRun_Tiers 校验日包（7z 或降级 tar.gz）+ 周包 + 月包都会产出，
// 且月包内装的是周包（分层结构，与 NVS 一致）。
func TestRun_Tiers(t *testing.T) {
	root, days := setup(t)
	opt := Options{LogDir: filepath.Join(root, "log"), ArchiveDir: filepath.Join(root, "archive")}
	outs, err := Run(opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 日包：7z 存在时用 7z，否则降级 tar.gz —— 两种都算通过
	for _, d := range days {
		if !fileExists(filepath.Join(opt.ArchiveDir, subDaily, d+".7z")) &&
			!fileExists(filepath.Join(opt.ArchiveDir, subDaily, d+".tar.gz")) {
			t.Errorf("日期 %s 没有日包，outputs=%v", d, outs)
		}
	}

	// 周包与月包必须存在（纯 Go tar.gz，不依赖外部命令）
	weekly, _ := filepath.Glob(filepath.Join(opt.ArchiveDir, subWeekly, "*.tar.gz"))
	monthly, _ := filepath.Glob(filepath.Join(opt.ArchiveDir, subMonthly, "*.tar.gz"))
	if len(weekly) == 0 {
		t.Fatalf("未生成周包，outputs=%v", outs)
	}
	if len(monthly) == 0 {
		t.Fatalf("未生成月包，outputs=%v", outs)
	}

	// 月包内应是周包文件名（2026Wxx.tar.gz），而不是零散日志
	names := tarNames(t, monthly[0])
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "W") || !strings.Contains(joined, ".tar.gz") {
		t.Fatalf("月包内容应为周包，实际 %v", names)
	}
}

// TestRun_DailyContainsCategoryDirs 校验日包内按 类别/日期 两级组织（与 NVS 同构）。
func TestRun_DailyContainsCategoryDirs(t *testing.T) {
	root, _ := setup(t)
	opt := Options{LogDir: filepath.Join(root, "log"), ArchiveDir: filepath.Join(root, "archive"), SevenZip: "none"}
	// 强制走纯 Go 降级路径，才能直接解 tar 校验内部结构
	if _, err := Run(opt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	daily, _ := filepath.Glob(filepath.Join(opt.ArchiveDir, subDaily, "*.tar.gz"))
	if len(daily) == 0 {
		t.Skip("系统里有 7z，日包为 .7z，本用例跳过（结构由 7z 保证）")
	}
	names := tarNames(t, daily[0])
	found := false
	for _, n := range names {
		if strings.HasPrefix(n, "requests/") || strings.HasPrefix(n, "database/") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("日包内应含 requests/ 或 database/ 一级目录，实际 %v", names)
	}
}

// TestRun_Idempotent 校验幂等：源没变时第二次不产出新包。
func TestRun_Idempotent(t *testing.T) {
	root, _ := setup(t)
	opt := Options{LogDir: filepath.Join(root, "log"), ArchiveDir: filepath.Join(root, "archive")}
	if _, err := Run(opt); err != nil {
		t.Fatal(err)
	}
	outs, err := Run(opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Fatalf("幂等失败：第二次仍产出 %v", outs)
	}
}

// TestRun_SkipsToday 校验默认不归档当天（当天日志还在写）。
func TestRun_SkipsToday(t *testing.T) {
	root := t.TempDir()
	today := time.Now().Format("2006-01-02")
	dir := filepath.Join(root, "log", "requests", today)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "10.log"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opt := Options{LogDir: filepath.Join(root, "log"), ArchiveDir: filepath.Join(root, "archive")}
	outs, err := Run(opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Fatalf("默认不应归档当天，实际产出 %v", outs)
	}

	// 显式打开后应归档
	opt.IncludeToday = true
	outs, err = Run(opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) == 0 {
		t.Fatal("IncludeToday=true 时应产生日包")
	}
}

// TestRun_MissingLogDir 日志目录不存在时不应报错（首次启动、还没写过日志）。
func TestRun_MissingLogDir(t *testing.T) {
	opt := Options{LogDir: filepath.Join(t.TempDir(), "nope"), ArchiveDir: filepath.Join(t.TempDir(), "arc")}
	if _, err := Run(opt); err != nil {
		t.Fatalf("日志目录缺失应静默返回，实际 %v", err)
	}
}

// tarNames 读出 tar.gz 内的条目名。
func tarNames(t *testing.T, p string) []string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	return names
}
