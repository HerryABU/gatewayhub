package stats

import (
	"math/rand"
	"sync"
	"time"

	"gorm.io/gorm"

	"gatewayhub/internal/geo"
	"gatewayhub/internal/models"
)

// Writer 异步日志写入器：Channel + Worker Pool + 批量插入
type Writer struct {
	db            *gorm.DB
	geo           *geo.Resolver
	ch            chan models.AccessLog
	workers       int
	batchSize     int
	batchInterval time.Duration
	sampleRate    float64
	bufferMax     int

	mu     sync.Mutex
	buffer []models.AccessLog
	stop   chan struct{}
	wg     sync.WaitGroup
	rngMu  sync.Mutex
	rng    *rand.Rand
}

// NewWriter 创建日志写入器
func NewWriter(db *gorm.DB, geo *geo.Resolver, workers, batchSize int, batchInterval time.Duration, sampleRate float64) *Writer {
	if workers <= 0 {
		workers = 4
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Writer{
		db:            db,
		geo:           geo,
		ch:            make(chan models.AccessLog, 10000),
		workers:       workers,
		batchSize:     batchSize,
		batchInterval: batchInterval,
		sampleRate:    sampleRate,
		bufferMax:     10000,
		stop:          make(chan struct{}),
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start 启动 worker pool 与定时刷写
func (w *Writer) Start() {
	for i := 0; i < w.workers; i++ {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			for log := range w.ch {
				w.process(log)
			}
		}()
	}
	w.wg.Add(1)
	go w.flusher()
}

// Enqueue 非阻塞入队（供转发热路径调用）
func (w *Writer) Enqueue(log models.AccessLog) {
	// 采样
	if w.sampleRate < 1.0 {
		w.rngMu.Lock()
		r := w.rng.Float64()
		w.rngMu.Unlock()
		if r > w.sampleRate {
			return
		}
	}
	select {
	case w.ch <- log:
	default:
		// 通道满则丢弃，避免阻塞转发
	}
}

func (w *Writer) process(log models.AccessLog) {
	// 解析地理位置
	if log.Country == "" && log.Province == "" {
		r := w.geo.Resolve(log.ClientIP)
		log.Country, log.CountryCode, log.Province, log.City = r.Country, r.CountryCode, r.Province, r.City
	}
	w.mu.Lock()
	w.buffer = append(w.buffer, log)
	if len(w.buffer) >= w.batchSize {
		w.flushLocked()
	}
	w.mu.Unlock()
}

func (w *Writer) flusher() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.batchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			w.flushLocked()
			w.mu.Unlock()
		case <-w.stop:
			return
		}
	}
}

func (w *Writer) flushLocked() {
	if len(w.buffer) == 0 {
		return
	}
	batch := w.buffer
	w.buffer = nil
	if err := w.db.CreateInBatches(batch, w.batchSize).Error; err != nil {
		// 失败降级：回填内存缓冲（最多 bufferMax 条），恢复后补写
		if len(w.buffer) < w.bufferMax {
			w.buffer = append(batch, w.buffer...)
		}
	}
}

// Stop 停止写入器并刷写剩余数据
func (w *Writer) Stop() {
	close(w.stop)
	close(w.ch)
	w.wg.Wait()
	w.mu.Lock()
	w.flushLocked()
	w.mu.Unlock()
}

// Cleanup 清理超过保留期的日志
func Cleanup(db *gorm.DB, retainDays int) (int64, error) {
	if retainDays <= 0 {
		retainDays = 180
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays)
	res := db.Where("created_at < ?", cutoff).Delete(&models.AccessLog{})
	return res.RowsAffected, res.Error
}
