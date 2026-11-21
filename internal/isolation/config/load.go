package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Table 是一次加载得到的「服务配置快照」：只读、可原子替换（热更新用）。
type Table struct {
	Version  int                 `json:"version"`
	Services []*Service          `json:"services"`
	Breaker  BreakerOptions      `json:"breaker"`
	Loop     RedirectLoopOptions `json:"redirect_loop"`
	Source   string              `json:"source"`
	Hash     string              `json:"hash"` // 配置文件内容指纹（审计用）
	LoadedAt time.Time           `json:"loaded_at"`
	Warnings []string            `json:"warnings,omitempty"`
}

// Count 返回启用中的服务数量。
func (t *Table) Count() int {
	if t == nil {
		return 0
	}
	n := 0
	for _, s := range t.Services {
		if s.Enabled {
			n++
		}
	}
	return n
}

// Load 读取并解析配置文件，返回校验通过的快照。
// 文件不存在时返回错误（由调用方决定降级策略：记录 config_error 并保持未启用）。
func Load(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取隔离配置失败: %w", err)
	}
	return parse(path, data)
}

// parse 从字节解析，便于测试与「带内容热加载」（避免读两次文件产生不一致）。
func parse(path string, data []byte) (*Table, error) {
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析隔离配置失败: %w", err)
	}
	services, err := f.Build()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return &Table{
		Version:  f.Version,
		Services: services,
		Breaker:  f.Breaker,
		Loop:     f.RedirectLoop,
		Source:   path,
		Hash:     hex.EncodeToString(sum[:8]),
		LoadedAt: time.Now(),
		Warnings: Warnings(services),
	}, nil
}

// Watcher 轮询配置文件 mtime/size，变化时重新加载并回调。
// 轮询而非 fsnotify：零依赖、跨平台一致、对编辑器「先写临时文件再重命名」的保存方式也有效。
type Watcher struct {
	path     string
	interval time.Duration
	mu       sync.Mutex
	lastHash string
	stop     chan struct{}
	once     sync.Once
}

// NewWatcher 创建配置监听器（interval <=0 时用 5s）。
func NewWatcher(path, initialHash string, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Watcher{path: path, interval: interval, lastHash: initialHash, stop: make(chan struct{})}
}

// Start 启动监听；文件内容变化时调用 onChange（只在内容真的变了才回调）。
func (w *Watcher) Start(onChange func(*Table, error)) {
	go func() {
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.check(onChange)
			}
		}
	}()
}

// Stop 停止监听（可重复调用）。
func (w *Watcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

// check 比对内容指纹，变化则重新解析并回调。
func (w *Watcher) check(onChange func(*Table, error)) {
	data, err := os.ReadFile(w.path)
	if err != nil {
		return // 文件暂时不可读（编辑器替换中）：下次再试，不打扰运维
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:8])
	w.mu.Lock()
	if hash == w.lastHash {
		w.mu.Unlock()
		return
	}
	w.lastHash = hash
	w.mu.Unlock()

	tbl, perr := parse(w.path, data)
	onChange(tbl, perr)
}
