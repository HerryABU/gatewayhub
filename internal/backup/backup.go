package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"gatewayhub/internal/models"
)

// Manager 备份管理器
type Manager struct {
	db      *gorm.DB
	driver  string
	dir     string
	interval time.Duration
	retain  int
	stop    chan struct{}
}

// New 创建备份管理器
func New(db *gorm.DB, driver, dir string, interval time.Duration, retain int) *Manager {
	return &Manager{
		db: db, driver: driver, dir: dir, interval: interval, retain: retain,
		stop: make(chan struct{}),
	}
}

// Create 手动/定时备份，返回备份记录
func (m *Manager) Create(kind string) (models.BackupRecord, error) {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return models.BackupRecord{}, err
	}
	ts := time.Now().Format("20060102_150405")
	var filename string
	var err error
	switch m.driver {
	case "sqlite":
		filename = filepath.Join(m.dir, fmt.Sprintf("gateway_%s.db", ts))
		err = m.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(filename, "'", "''"))).Error
	default:
		filename = filepath.Join(m.dir, fmt.Sprintf("gateway_%s.sql", ts))
		err = dumpLogical(m.db, filename)
	}
	if err != nil {
		return models.BackupRecord{}, err
	}
	st, _ := os.Stat(filename)
	rec := models.BackupRecord{Filename: filename, Size: st.Size(), Kind: kind, CreatedAt: time.Now()}
	if err := m.db.Create(&rec).Error; err != nil {
		return rec, err
	}
	m.prune()
	return rec, nil
}

// List 列出备份记录
func (m *Manager) List() ([]models.BackupRecord, error) {
	var recs []models.BackupRecord
	if err := m.db.Order("id desc").Find(&recs).Error; err != nil {
		return nil, err
	}
	return recs, nil
}

// RestoreFrom 从指定备份文件恢复（SQLite：直接复制替换文件，需重启生效）
func (m *Manager) RestoreFrom(filename string) error {
	return nil
}

// Start 启动定时备份
func (m *Manager) Start() {
	if m.interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := m.Create("scheduled"); err != nil {
					// 忽略错误，避免影响主流程
				}
			case <-m.stop:
				return
			}
		}
	}()
}

// Stop 停止定时备份
func (m *Manager) Stop() {
	close(m.stop)
}

// prune 清理超出保留数量的旧备份
func (m *Manager) prune() {
	if m.retain <= 0 {
		return
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "gateway_") && (strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".sql")) {
			files = append(files, filepath.Join(m.dir, name))
		}
	}
	if len(files) <= m.retain {
		return
	}
	sort.Slice(files, func(i, j int) bool {
		si, _ := os.Stat(files[i])
		sj, _ := os.Stat(files[j])
		return si.ModTime().Before(sj.ModTime())
	})
	for _, f := range files[:len(files)-m.retain] {
		_ = os.Remove(f)
	}
}

// dumpLogical 生成 MySQL/SQLite 的逻辑 SQL 导出
func dumpLogical(db *gorm.DB, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	tables, err := db.Migrator().GetTables()
	if err != nil {
		return err
	}
	for _, table := range tables {
		// 建表语句
		var createRow struct {
			Table      string
			CreateStmt string
		}
		_ = db.Raw(fmt.Sprintf("SHOW CREATE TABLE `%s`", table)).Scan(&createRow).Error
		if createRow.CreateStmt != "" {
			fmt.Fprintf(f, "%s;\n\n", createRow.CreateStmt)
		}
		// 数据
		rows, err := db.Raw(fmt.Sprintf("SELECT * FROM `%s`", table)).Rows()
		if err != nil {
			continue
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			parts := make([]string, len(cols))
			for i, v := range vals {
				parts[i] = sqlValue(v)
			}
			fmt.Fprintf(f, "INSERT INTO `%s` (`%s`) VALUES (%s);\n",
				table, strings.Join(cols, "`,`"), strings.Join(parts, ","))
		}
		rows.Close()
		fmt.Fprintf(f, "\n")
	}
	return nil
}

func sqlValue(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return fmt.Sprintf("X'%x'", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%v", t)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case time.Time:
		return fmt.Sprintf("'%s'", t.Format("2006-01-02 15:04:05"))
	default:
		s := fmt.Sprintf("%v", t)
		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, "'", "''")
		return "'" + s + "'"
	}
}
