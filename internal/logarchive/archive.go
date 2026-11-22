// Package logarchive 日志分层归档（结构与压缩方式对齐 NVS 的 server/logarchive）：
//
//	① 每天：当天所有类别（requests / database）的日志目录打成一个包 →  daily/<date>.7z
//	② 每周后：把该周的日包打成 1 个 tar.gz            →  weekly/<YYYY>W<WW>.tar.gz
//	③ 每月后：把该月的周包打成 1 个 tar.gz            →  monthly/<YYYY-MM>.tar.gz
//	④ 每年后：把该年的月包打成 1 个 tar.xz            →  yearly/<YYYY>.tar.xz
//
// 归档粒度是「日期」，不按类别独立成包：请求日志与数据库日志同日同包（包内仍按
// 类别/日期 两级目录组织），与 NVS 完全一致，方便两边共用解包/检索习惯。
//
// 幂等：产物比源新则跳过。7z / tar / xz 缺失时按层级降级（日层降级为 tar.gz，
// 年层缺失 xz 则跳过），绝不因为缺一个外部命令就整条链断掉。
//
// 与 NVS 的一处差异：默认**不归档当天**（IncludeToday=false）。当天的日志还在写，
// 每次定时归档都会因为目录 mtime 变化重打一遍当天的包，纯属浪费——昨天的日志在
// 今天必然已经收尾。需要连当天一起打时把 IncludeToday 置 true。
package logarchive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 归档目录名（位于 ArchiveDir 之下，与 NVS 一致）
const (
	subDaily   = "daily"
	subWeekly  = "weekly"
	subMonthly = "monthly"
	subYearly  = "yearly"
)

// Options 归档参数。
type Options struct {
	LogDir       string // 日志根目录（含 requests/、database/ 等类别目录）
	ArchiveDir   string // 归档输出目录，默认 {LogDir}/archive
	SevenZip     string // 7z 可执行文件；空则自动探测
	IncludeToday bool   // 是否归档当天（默认 false：当天日志仍在写入）
}

// Resolve 补齐缺省值（日志目录、归档目录、7z 路径），返回可直接使用的参数。
func (o Options) Resolve() Options {
	if strings.TrimSpace(o.LogDir) == "" {
		o.LogDir = "log"
	}
	if strings.TrimSpace(o.ArchiveDir) == "" {
		o.ArchiveDir = filepath.Join(o.LogDir, "archive")
	}
	if strings.TrimSpace(o.SevenZip) == "" {
		o.SevenZip = find7z()
	}
	// SevenZip="none" 表示显式禁用 7z（走纯 Go tar.gz 降级），供测试与无 7z 环境固定行为
	if strings.EqualFold(strings.TrimSpace(o.SevenZip), "none") {
		o.SevenZip = ""
	}
	return o
}

// SevenZipPath 返回探测到的 7z 路径（空串=未找到，日层会降级为 tar.gz）。
func SevenZipPath() string { return find7z() }

func find7z() string {
	for _, p := range []string{"7z", "7za", `C:\Program Files\7-Zip\7z.exe`, `C:\Program Files (x86)\7-Zip\7z.exe`} {
		if lp, err := exec.LookPath(p); err == nil {
			return lp
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Run 执行分层归档，返回本次新建的产物列表。单层失败不中断整体，错误汇总返回。
func Run(opt Options) ([]string, error) {
	opt = opt.Resolve()
	var outputs []string
	var errs []string
	addErr := func(format string, a ...interface{}) { errs = append(errs, fmt.Sprintf(format, a...)) }

	dateTypes, dates, err := collectDates(opt.LogDir)
	if err != nil {
		return outputs, err
	}
	if !opt.IncludeToday {
		today := time.Now().Format("2006-01-02")
		kept := dates[:0]
		for _, d := range dates {
			if d != today {
				kept = append(kept, d)
			}
		}
		dates = kept
	}
	if len(dates) == 0 {
		return outputs, nil
	}

	// ① 日包：同一天的所有类别打在一起（7z 优先，缺 7z 时降级 tar.gz）
	dailyOut := map[string]string{}
	for _, d := range dates {
		srcDirs := make([]string, 0, len(dateTypes[d]))
		for _, typ := range dateTypes[d] {
			srcDirs = append(srcDirs, filepath.Join(opt.LogDir, typ, d))
		}
		if len(srcDirs) == 0 {
			continue
		}
		out7z := filepath.Join(opt.ArchiveDir, subDaily, d+".7z")
		outGz := filepath.Join(opt.ArchiveDir, subDaily, d+".tar.gz")

		if opt.SevenZip != "" {
			if staleAny(srcDirs, out7z) {
				if err := pack7zDirs(opt.SevenZip, out7z, srcDirs); err != nil {
					addErr("daily %s: %v", out7z, err)
				} else {
					outputs = append(outputs, out7z)
				}
			}
			if fileExists(out7z) {
				dailyOut[d] = out7z
				continue
			}
		}
		// 降级：纯 Go 打 tar.gz（不依赖外部命令），包内同样是 类别/日期 两级目录
		if staleAny(srcDirs, outGz) {
			if err := packDirsTarGz(outGz, srcDirs); err != nil {
				addErr("daily(fallback) %s: %v", outGz, err)
				continue
			}
			outputs = append(outputs, outGz)
		}
		if fileExists(outGz) {
			dailyOut[d] = outGz
		}
	}

	// ② 周包（内容 = 该周各日的日包）
	weeklyOut := map[string]string{}
	for _, w := range groupWeek(dates) {
		srcs := pickExisting(dailyOut, w.dates)
		if len(srcs) == 0 {
			continue
		}
		out := filepath.Join(opt.ArchiveDir, subWeekly, w.key+".tar.gz")
		if staleAny(srcs, out) {
			if err := packFilesTarGz(out, srcs); err != nil {
				addErr("weekly %s: %v", out, err)
			} else {
				outputs = append(outputs, out)
			}
		}
		if fileExists(out) {
			weeklyOut[w.key] = out
		}
	}

	// ③ 月包（内容 = 该月各周的周包）
	monthlyOut := map[string]string{}
	for _, m := range groupMonth(dates) {
		var srcs []string
		for _, wk := range weeklyOut {
			if weekInMonth(filepath.Base(wk), m.key) {
				srcs = append(srcs, wk)
			}
		}
		if len(srcs) == 0 {
			continue
		}
		out := filepath.Join(opt.ArchiveDir, subMonthly, m.key+".tar.gz")
		if staleAny(srcs, out) {
			if err := packFilesTarGz(out, srcs); err != nil {
				addErr("monthly %s: %v", out, err)
			} else {
				outputs = append(outputs, out)
			}
		}
		if fileExists(out) {
			monthlyOut[m.key] = out
		}
	}

	// ④ 年包（内容 = 全年各月的月包，tar.xz 压缩率最高）
	for _, y := range groupYear(dates) {
		var srcs []string
		for mk, p := range monthlyOut {
			if strings.HasPrefix(mk, y.key+"-") {
				srcs = append(srcs, p)
			}
		}
		if len(srcs) == 0 {
			continue
		}
		out := filepath.Join(opt.ArchiveDir, subYearly, y.key+".tar.xz")
		if staleAny(srcs, out) {
			if err := packFilesTarXz(out, srcs); err != nil {
				// 缺 tar/xz 只降级：月包仍然可用，不做成整轮失败
				addErr("yearly %s: %v", out, err)
				continue
			}
			outputs = append(outputs, out)
		}
	}

	if len(errs) > 0 {
		return outputs, fmt.Errorf("%d 个归档失败: %s", len(errs), strings.Join(errs, "; "))
	}
	return outputs, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func pickExisting(m map[string]string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok && fileExists(v) {
			out = append(out, v)
		}
	}
	return out
}

// collectDates 返回「日期 → 当天有日志的类别列表」与排序后的日期列表。
// 日志目录还不存在（首次启动、尚未写过日志）视为「没有日志」，不报错。
func collectDates(logDir string) (map[string][]string, []string, error) {
	types, err := listTypes(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, nil, nil
		}
		return nil, nil, err
	}
	dateTypes := map[string][]string{}
	for _, typ := range types {
		dates, err := listDates(filepath.Join(logDir, typ))
		if err != nil {
			continue
		}
		for _, d := range dates {
			dateTypes[d] = append(dateTypes[d], typ)
		}
	}
	dates := make([]string, 0, len(dateTypes))
	for d := range dateTypes {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dateTypes, dates, nil
}

// weekRe 从周归档名（如 2026W45.tar.gz）提取年/周
var weekRe = regexp.MustCompile(`(\d{4})W(\d{1,2})`)

// weekInMonth 判断周归档归属的月份（按该周周一所在月）。
func weekInMonth(base, monthKey string) bool {
	m := weekRe.FindStringSubmatch(base)
	if len(m) != 3 {
		return false
	}
	t, err := weekStart(m[1], m[2])
	if err != nil {
		return false
	}
	return t.Format("2006-01") == monthKey
}

func weekStart(year, week string) (time.Time, error) {
	y, err := parseInt(year)
	if err != nil {
		return time.Time{}, err
	}
	w, err := parseInt(week)
	if err != nil {
		return time.Time{}, err
	}
	jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.Local)
	wd := int(jan4.Weekday())
	if wd == 0 {
		wd = 7
	}
	monday := jan4.AddDate(0, 0, -(wd - 1))
	return monday.AddDate(0, 0, (w-1)*7), nil
}

func parseInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("bad number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func listTypes(logDir string) ([]string, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		// archive 自身不能被当成日志类别，否则会归档归档件
		if e.IsDir() && e.Name() != "archive" && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func listDates(typeDir string) ([]string, error) {
	entries, err := os.ReadDir(typeDir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := time.Parse("2006-01-02", e.Name()); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

type weekGroup struct {
	key   string
	dates []string
}

func groupWeek(dates []string) []weekGroup {
	m := map[string][]string{}
	var order []string
	for _, d := range dates {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		key := fmt.Sprintf("%dW%02d", y, w)
		if _, ok := m[key]; !ok {
			order = append(order, key)
		}
		m[key] = append(m[key], d)
	}
	sort.Strings(order)
	out := make([]weekGroup, 0, len(order))
	for _, k := range order {
		out = append(out, weekGroup{key: k, dates: m[k]})
	}
	return out
}

type monthGroup struct {
	key   string
	dates []string
}

func groupMonth(dates []string) []monthGroup {
	m := map[string][]string{}
	var order []string
	for _, d := range dates {
		key := d[:7]
		if _, ok := m[key]; !ok {
			order = append(order, key)
		}
		m[key] = append(m[key], d)
	}
	sort.Strings(order)
	out := make([]monthGroup, 0, len(order))
	for _, k := range order {
		out = append(out, monthGroup{key: k, dates: m[k]})
	}
	return out
}

type yearGroup struct {
	key   string
	dates []string
}

func groupYear(dates []string) []yearGroup {
	m := map[string][]string{}
	var order []string
	for _, d := range dates {
		key := d[:4]
		if _, ok := m[key]; !ok {
			order = append(order, key)
		}
		m[key] = append(m[key], d)
	}
	sort.Strings(order)
	out := make([]yearGroup, 0, len(order))
	for _, k := range order {
		out = append(out, yearGroup{key: k, dates: m[k]})
	}
	return out
}

// staleAny 任一源目录比产物新 → 需要重建。
func staleAny(srcDirs []string, out string) bool {
	oi, err := os.Stat(out)
	if err != nil {
		return true
	}
	for _, d := range srcDirs {
		if si, err := os.Stat(d); err == nil && si.ModTime().After(oi.ModTime()) {
			return true
		}
	}
	return false
}

// pack7zDirs 把多个「<类别>/<日期>」目录打进同一个 7z：包内结构为 <类别>/<日期>/...
func pack7zDirs(sevenZip, out string, srcDirs []string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "gw-log7z-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, src := range srcDirs {
		typ := filepath.Base(filepath.Dir(src)) // 类别 = 父目录名
		if err := copyTree(src, filepath.Join(tmp, typ, filepath.Base(src))); err != nil {
			return err
		}
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		absOut = out
	}
	// -mx=5：日志是文本，5 级在体积与耗时之间最划算（与 NVS 一致）
	cmd := exec.Command(sevenZip, "a", "-t7z", "-mx=5", "-y", absOut, ".")
	cmd.Dir = tmp
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("7z: %v: %s", err, string(b))
	}
	return nil
}

// packDirsTarGz 是日包的降级实现：纯 Go 打 tar.gz，包内保持 <类别>/<日期>/... 两级结构。
func packDirsTarGz(out string, srcDirs []string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, src := range srcDirs {
		// src 形如 <log>/<类别>/<日期>；从 <log> 根算相对路径，包内即 类别/日期/文件
		root := filepath.Dir(filepath.Dir(src))
		err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			if info.IsDir() {
				return nil // 只写文件条目，目录由解包工具按需创建
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = name
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, in)
			in.Close()
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// packFilesTarGz 把若干「上一级归档文件」打包为 tar.gz（保留文件名）。
func packFilesTarGz(out string, files []string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			continue
		}
		hdr.Name = filepath.Base(file)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(file)
		if err != nil {
			return err
		}
		_, _ = io.Copy(tw, in)
		in.Close()
	}
	return nil
}

// packFilesTarXz 把若干「上一级归档文件」打包为 tar.xz（依赖系统 tar + xz）。
func packFilesTarXz(out string, files []string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		absOut = out
	}
	tmp, err := os.MkdirTemp("", "gw-logxz-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	names := make([]string, 0, len(files))
	for _, f := range files {
		if err := copyFile(f, filepath.Join(tmp, filepath.Base(f))); err != nil {
			return err
		}
		names = append(names, filepath.Base(f))
	}
	args := append([]string{"-cJf", toPosixPath(absOut), "-C", toPosixPath(tmp)}, names...)
	cmd := exec.Command("tar", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar.xz: %v: %s", err, string(b))
	}
	return nil
}

// toPosixPath 把 Windows 路径转成 MSYS/GNU tar 能识别的 POSIX 形式（C:\x → /c/x），
// 否则 tar 会把 "C:" 当成远程主机名（GNU tar 1.35 实测如此）。
func toPosixPath(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		return "/" + strings.ToLower(p[:1]) + strings.ReplaceAll(p[2:], "\\", "/")
	}
	return strings.ReplaceAll(p, "\\", "/")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}
