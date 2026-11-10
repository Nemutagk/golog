package models

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingDriver struct {
	delay time.Duration
	mu    sync.Mutex
	docs  []map[string]any
}

func (d *recordingDriver) Create(ctx context.Context, document map[string]any) error {
	time.Sleep(d.delay)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs = append(d.docs, document)
	return nil
}

func (d *recordingDriver) CreateMany(ctx context.Context, documents []map[string]any) error {
	time.Sleep(d.delay)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs = append(d.docs, documents...)
	return nil
}

func (d *recordingDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.docs)
}

func TestBatchDriverCloseFlushesPending(t *testing.T) {
	// El driver tarda para que el test falle si Close no espera el flush final.
	under := &recordingDriver{delay: 20 * time.Millisecond}
	bd := NewBatchDriver(under, 100, time.Hour).(*batchDriver)

	if err := bd.Create(context.Background(), map[string]any{"n": 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := under.count(); got != 0 {
		t.Fatalf("el documento no debía persistirse antes de Close: %d", got)
	}

	bd.Close()

	if got := under.count(); got != 1 {
		t.Fatalf("Close debía persistir el batch pendiente: got %d", got)
	}
}

func TestBatchDriverFlushBySizeDoesNotBlockCaller(t *testing.T) {
	under := &recordingDriver{delay: 300 * time.Millisecond}
	bd := NewBatchDriver(under, 2, time.Hour).(*batchDriver)
	defer bd.Close()

	start := time.Now()
	_ = bd.Create(context.Background(), map[string]any{"n": 1})
	_ = bd.Create(context.Background(), map[string]any{"n": 2}) // llena el batch
	if el := time.Since(start); el > 150*time.Millisecond {
		t.Fatalf("Create no debe bloquearse por el flush: tardó %v", el)
	}

	deadline := time.Now().Add(2 * time.Second)
	for under.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := under.count(); got != 2 {
		t.Fatalf("el flush por tamaño debía ocurrir en segundo plano: got %d", got)
	}
}

func TestBatchDriverKeepsOrder(t *testing.T) {
	under := &recordingDriver{delay: time.Millisecond}
	bd := NewBatchDriver(under, 3, 5*time.Millisecond).(*batchDriver)

	for i := 0; i < 40; i++ {
		_ = bd.Create(context.Background(), map[string]any{"n": i})
		if i%7 == 0 {
			time.Sleep(6 * time.Millisecond) // mezcla flush por tamaño y por intervalo
		}
	}
	bd.Close()

	if under.count() != 40 {
		t.Fatalf("se esperaban 40, got %d", under.count())
	}
	for i, d := range under.docs {
		if d["n"] != i {
			t.Fatalf("orden roto en %d: %v", i, d["n"])
		}
	}
}

func TestBatchDriverDropsNewDocsWhenFull(t *testing.T) {
	under := &gateDriver{gate: make(chan struct{})}
	bd := NewBatchDriver(under, 2, time.Hour).(*batchDriver)
	bd.max = 4

	start := time.Now()
	for i := 0; i < 10; i++ {
		_ = bd.Create(context.Background(), map[string]any{"n": i})
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("con el buffer lleno Create no debe bloquear: %v", el)
	}

	close(under.gate)
	bd.Close()

	if got := under.count(); got != 4 {
		t.Fatalf("se esperaban 4 documentos (tope), got %d", got)
	}
	for i, d := range under.docs {
		if d["n"] != i {
			t.Fatalf("orden roto en %d: %v", i, d["n"])
		}
	}
}

func TestBatchDriverPanicDoesNotKillFlushGoroutine(t *testing.T) {
	bd := NewBatchDriver(panickingDriver{}, 2, 5*time.Millisecond).(*batchDriver)

	for i := 0; i < 3; i++ {
		if err := bd.Create(context.Background(), map[string]any{"n": i}); err != nil {
			t.Fatal(err)
		}
	}

	// Sin recover, el panic del driver mataba a la goroutine de fondo y Close
	// colgaba para siempre esperando <-done.
	done := make(chan struct{})
	go func() {
		bd.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close no retornó: la goroutine de flush murió por el panic del driver")
	}
}

func TestBatchDriverStuckDriverTimeoutLetsCloseReturn(t *testing.T) {
	bd := NewBatchDriver(&stuckDriver{}, 2, time.Hour).(*batchDriver)
	bd.driverTimeout = 50 * time.Millisecond

	if err := bd.Create(context.Background(), map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		bd.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close no retornó: el driver atascado bloqueó el flush indefinidamente")
	}
}

// Sin el arreglo, los documentos que llegaban tras Close() se acumulaban en
// el buffer sin que nadie los volcara: pérdida silenciosa.
func TestBatchDriverCreateAfterCloseReachesDriver(t *testing.T) {
	under := &recordingDriver{}
	bd := NewBatchDriver(under, 100, time.Hour).(*batchDriver)

	bd.Close()

	if err := bd.Create(context.Background(), map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := bd.CreateMany(context.Background(), []map[string]any{{"n": 2}, {"n": 3}}); err != nil {
		t.Fatal(err)
	}

	// Inserción directa: ya no dependen de ninguna goroutine de flush.
	if got := under.count(); got != 3 {
		t.Fatalf("los Create tras Close debían llegar directo al driver: got %d", got)
	}
	for i, d := range under.docs {
		if d["n"] != i+1 {
			t.Fatalf("orden roto en %d: %v", i+1, d["n"])
		}
	}
}

func TestBatchDriverConcurrentCreateCloseKeepsAllDocs(t *testing.T) {
	under := &recordingDriver{}
	bd := NewBatchDriver(under, 10, time.Hour).(*batchDriver)
	bd.max = 500 // por encima de los envíos: ningún documento debe descartarse

	const sends = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < sends; i++ {
			_ = bd.Create(context.Background(), map[string]any{"n": i})
		}
	}()
	go func() {
		defer wg.Done()
		bd.Close()
	}()
	wg.Wait()

	if got := under.count(); got != sends {
		t.Fatalf("se perdieron o duplicaron documentos: enviados %d, llegados %d", sends, got)
	}
}

func TestBatchDriverNonPositiveIntervalUsesDefault(t *testing.T) {
	bd := NewBatchDriver(&recordingDriver{}, 2, 0).(*batchDriver)
	defer bd.Close()
	if bd.interval != 100*time.Millisecond {
		t.Fatalf("interval = %v", bd.interval)
	}
}
