package models

import (
	"context"
	"log"
	"sync"
	"time"
)

// batchDriver envuelve un Driver y agrupa documentos
type batchDriver struct {
	under    Driver
	size     int
	interval time.Duration

	mu    sync.Mutex
	buf   []map[string]any
	timer *time.Timer
	stop  chan struct{}
	once  sync.Once
}

func NewBatchDriver(under Driver, size int, flushInterval time.Duration) Driver {
	if size <= 1 {
		return under
	}
	bd := &batchDriver{
		under:    under,
		size:     size,
		interval: flushInterval,
		buf:      make([]map[string]any, 0, size),
		stop:     make(chan struct{}),
	}
	bd.start()
	return bd
}

func (b *batchDriver) start() {
	b.timer = time.NewTimer(b.interval)
	go func() {
		for {
			select {
			case <-b.timer.C:
				b.flush(context.Background())
				b.resetTimer()
			case <-b.stop:
				b.flush(context.Background())
				return
			}
		}
	}()
}

func (b *batchDriver) resetTimer() {
	if !b.timer.Stop() {
		select {
		case <-b.timer.C:
		default:
		}
	}
	b.timer.Reset(b.interval)
}

func (b *batchDriver) Create(ctx context.Context, document map[string]any) error {
	b.mu.Lock()
	b.buf = append(b.buf, document)
	full := len(b.buf) >= b.size
	b.mu.Unlock()

	if full {
		b.flush(ctx)
	}
	return nil
}

func (b *batchDriver) CreateMany(ctx context.Context, documents []map[string]any) error {
	b.mu.Lock()
	b.buf = append(b.buf, documents...)
	needFlush := len(b.buf) >= b.size
	b.mu.Unlock()

	if needFlush {
		b.flush(ctx)
	}
	return nil
}

func (b *batchDriver) flush(ctx context.Context) {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.buf
	b.buf = make([]map[string]any, 0, b.size)
	b.mu.Unlock()

	// Intentar bulk si disponible
	if bi, ok := b.under.(BulkInserter); ok {
		if err := bi.CreateMany(ctx, batch); err != nil {
			log.Printf("Batch insert error (bulk) %T: %v (falling back)", b.under, err)
			for _, d := range batch {
				_ = b.under.Create(ctx, d)
			}
		}
		return
	}
	for _, d := range batch {
		_ = b.under.Create(ctx, d)
	}
}

func (b *batchDriver) Close() {
	b.once.Do(func() {
		close(b.stop)
	})
}
