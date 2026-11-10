package models

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// batchDriver envuelve un Driver y agrupa documentos
type batchDriver struct {
	under    Driver
	size     int
	interval time.Duration

	mu     sync.Mutex
	closed bool // protegido por mu; tras Close, add inserta directo sin buffer
	buf    []map[string]any
	timer  *time.Timer
	stop   chan struct{}
	done   chan struct{}
	full   chan struct{} // avisa a la goroutine que el buffer alcanzó size

	max           int           // tope de documentos pendientes (en buffer + en escritura)
	pending       int           // pendientes; protegido por mu
	dropped       atomic.Int64  // descartados desde el último aviso
	driverTimeout time.Duration // timeout por llamada al driver subyacente
	once          sync.Once
}

func NewBatchDriver(under Driver, size int, flushInterval time.Duration) Driver {
	if size <= 1 {
		return under
	}
	if flushInterval <= 0 {
		flushInterval = 100 * time.Millisecond // un timer de 0 ms dejaría la goroutine al 100 % de CPU
	}
	bd := &batchDriver{
		under:         under,
		size:          size,
		interval:      flushInterval,
		buf:           make([]map[string]any, 0, size),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		full:          make(chan struct{}, 1),
		max:           max(queueLimit(), size),
		driverTimeout: driverTimeout(),
	}
	bd.start()
	return bd
}

func (b *batchDriver) start() {
	b.timer = time.NewTimer(b.interval)
	go func() {
		defer close(b.done)
		for {
			select {
			case <-b.timer.C:
				b.flush(context.Background())
				b.resetTimer()
			case <-b.full:
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
	b.add(document)
	return nil
}

func (b *batchDriver) CreateMany(ctx context.Context, documents []map[string]any) error {
	b.add(documents...)
	return nil
}

// add encola sin esperar: el flush (por tamaño o intervalo) lo hace siempre la
// goroutine de fondo, una sola, así el orden se conserva y el llamador nunca se bloquea.
func (b *batchDriver) add(docs ...map[string]any) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		// Tras Close la goroutine de fondo ya no vuelca el buffer: insertar
		// directo (síncrono y en orden) para no perder estos documentos.
		b.insertOneByOne(context.Background(), docs)
		return
	}
	if room := b.max - b.pending; len(docs) > room {
		b.dropped.Add(int64(len(docs) - room))
		docs = docs[:room]
	}
	b.buf = append(b.buf, docs...)
	b.pending += len(docs)
	n := len(b.buf)
	b.mu.Unlock()

	if n >= b.size {
		select {
		case b.full <- struct{}{}:
		default: // ya hay un aviso pendiente
		}
	}
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
	defer func() {
		b.mu.Lock()
		b.pending -= len(batch)
		b.mu.Unlock()
		reportDropped(&b.dropped, "buffer")
	}()

	// Intentar bulk si disponible
	if bi, ok := b.under.(BulkInserter); ok {
		if err := b.callMany(ctx, bi, batch); err != nil {
			log.Printf("Batch insert error (bulk) %T: %v (falling back)", b.under, err)
			b.insertOneByOne(ctx, batch)
		}
		return
	}
	b.insertOneByOne(ctx, batch)
}

// insertOneByOne entrega cada documento por separado y cuenta los fallos: el
// error no se propaga porque provocaría que el lote se reintente completo y
// duplicara los documentos ya insertados.
func (b *batchDriver) insertOneByOne(ctx context.Context, batch []map[string]any) {
	failed := 0
	for _, d := range batch {
		if err := b.callCreate(ctx, d); err != nil {
			failed++
		}
	}
	if failed > 0 {
		log.Printf("golog: %d/%d logs no se pudieron insertar (%T)", failed, len(batch), b.under)
	}
}

// callCreate aplica timeout y recover a Create: un driver atascado o que haga
// panic no debe tumbar la goroutine de fondo. El panic se reporta como error
// para que cuente como fallo en insertOneByOne.
func (b *batchDriver) callCreate(ctx context.Context, doc map[string]any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("golog: panic in driver %T: %v", b.under, r)
			err = fmt.Errorf("driver panic: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, b.driverTimeout)
	defer cancel()
	return b.under.Create(ctx, doc)
}

// callMany aplica timeout y recover a CreateMany. Tras un panic NO se cae al
// fallback uno a uno: reintentar el lote duplicaría lo ya insertado.
func (b *batchDriver) callMany(ctx context.Context, bi BulkInserter, batch []map[string]any) error {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("golog: panic in driver %T: %v", b.under, r)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, b.driverTimeout)
	defer cancel()
	return bi.CreateMany(ctx, batch)
}

func (b *batchDriver) Close() {
	b.once.Do(func() {
		// Marcar cerrado bajo el lock ANTES de cerrar stop: los add() que
		// todavía no lo vieron caen en el buffer, que el flush final de la
		// goroutine sí vuelca; los que lleguen después insertan directo.
		b.mu.Lock()
		b.closed = true
		b.mu.Unlock()
		close(b.stop)
	})
	<-b.done
}
