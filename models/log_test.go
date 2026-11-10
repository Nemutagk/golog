package models

import (
	"context"
	"errors"
	"github.com/Nemutagk/golog/helper"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestAsyncCreateLogAfterCloseReturnsError(t *testing.T) {
	s := NewAsyncService(1, 4, &recordingDriver{})

	if err := s.CreateLog(context.Background(), Logger{Level: "INFO"}); err != nil {
		t.Fatalf("CreateLog antes de Close: %v", err)
	}

	s.Close()

	if err := s.CreateLog(context.Background(), Logger{Level: "INFO"}); err == nil {
		t.Fatal("CreateLog después de Close debía devolver error")
	}
}

// countingPayload cuenta sus serializaciones: sanitize() invoca json.Marshal
// para decidir si un struct es serializable, así que cada sanitización del
// payload incrementa el contador exactamente una vez.
type countingPayload struct{ calls *atomic.Int64 }

func (c countingPayload) MarshalJSON() ([]byte, error) {
	c.calls.Add(1)
	return []byte(`{}`), nil
}

// Sin el arreglo, CreateLog sanitizaba ANTES de comprobar cerrado/lleno: bajo
// sobrecarga el llamador pagaba la reflexión por logs que se descartaban.
func TestAsyncDroppedLogIsNotSanitized(t *testing.T) {
	under := &gateDriver{gate: make(chan struct{})}
	s := NewAsyncService(1, 4, under)
	s.max = 3

	var calls atomic.Int64
	for i := 0; i < 10; i++ {
		if err := s.CreateLog(context.Background(), Logger{Payload: []any{countingPayload{calls: &calls}}}); err != nil {
			t.Fatal(err)
		}
	}

	close(under.gate)
	s.Close()

	// Solo los 3 aceptados se sanitizan; los 7 descartados por cola llena no.
	if got := calls.Load(); got != 3 {
		t.Fatalf("la sanitización corrió %d veces; solo los 3 logs aceptados debían sanitizarse", got)
	}
	if got := under.count(); got != 3 {
		t.Fatalf("se esperaban 3 logs (tope), got %d", got)
	}
}

func TestAsyncCreateLogAfterCloseDoesNotSanitize(t *testing.T) {
	s := NewAsyncService(1, 4, &recordingDriver{})
	var calls atomic.Int64

	s.Close()

	if err := s.CreateLog(context.Background(), Logger{Payload: []any{countingPayload{calls: &calls}}}); err == nil {
		t.Fatal("CreateLog después de Close debía devolver error")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("un log rechazado por servicio cerrado no debía sanitizarse: %d llamadas", got)
	}
}

// El modo síncrono sanitiza y procesa igual que antes del cambio de orden.
func TestSyncCreateLogStillSanitizes(t *testing.T) {
	under := &recordingDriver{}
	s := NewService(under)
	var calls atomic.Int64

	if err := s.CreateLog(context.Background(), Logger{Payload: []any{countingPayload{calls: &calls}}}); err != nil {
		t.Fatal(err)
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("el modo síncrono debía sanitizar el payload: %d llamadas", got)
	}
	if got := under.count(); got != 1 {
		t.Fatalf("el driver no recibió el log: got %d", got)
	}
}

func TestAsyncKeepsOrderAndDoesNotBlockCaller(t *testing.T) {
	// Driver lento: si el llamador esperara, 50 logs tardarían ~250ms.
	under := &recordingDriver{delay: 5 * time.Millisecond}
	s := NewAsyncService(8, 2, under) // cola inicial pequeña: debe crecer sin bloquear

	start := time.Now()
	for i := 0; i < 50; i++ {
		if err := s.CreateLog(context.Background(), Logger{RequestID: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("CreateLog no debe bloquear al llamador: tardó %v", el)
	}

	s.Close()

	if got := under.count(); got != 50 {
		t.Fatalf("se esperaban 50 logs, got %d", got)
	}
	for i, d := range under.docs {
		if d["request_id"] != strconv.Itoa(i) {
			t.Fatalf("orden roto en %d: %v", i, d["request_id"])
		}
	}
}

func TestAsyncSurvivesCancelledRequestContext(t *testing.T) {
	under := &recordingDriver{delay: 20 * time.Millisecond}
	s := NewAsyncService(1, 4, under)
	ctx, cancel := context.WithCancel(context.Background())
	_ = s.CreateLog(ctx, Logger{})
	cancel()
	s.Close()
	if under.count() != 1 {
		t.Fatal("el log debía persistirse aunque se cancelara el contexto de la petición")
	}
}

type closeCounter struct {
	recordingDriver
	closes int
}

func (c *closeCounter) Close() { c.closes++ }

func TestServiceCloseIsIdempotent(t *testing.T) {
	for _, s := range []*Service{NewService(), NewAsyncService(1, 1)} {
		c := &closeCounter{}
		s.drivers = []Driver{c}
		s.Close()
		s.Close()
		if c.closes != 1 {
			t.Fatalf("async=%v: Close del driver llamado %d veces", s.async, c.closes)
		}
	}
}

// gateDriver bloquea Create hasta que se abre gate (simula un driver atascado).
type gateDriver struct {
	recordingDriver
	gate chan struct{}
}

func (g *gateDriver) Create(ctx context.Context, d map[string]any) error {
	<-g.gate
	return g.recordingDriver.Create(ctx, d)
}

func (g *gateDriver) CreateMany(ctx context.Context, ds []map[string]any) error {
	<-g.gate
	return g.recordingDriver.CreateMany(ctx, ds)
}

func TestAsyncDropsNewLogsWhenQueueIsFull(t *testing.T) {
	under := &gateDriver{gate: make(chan struct{})}
	s := NewAsyncService(1, 4, under)
	s.max = 3

	start := time.Now()
	for i := 0; i < 10; i++ {
		if err := s.CreateLog(context.Background(), Logger{RequestID: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("con la cola llena CreateLog no debe bloquear: %v", el)
	}

	close(under.gate)
	s.Close()

	if got := under.count(); got != 3 {
		t.Fatalf("se esperaban 3 logs (tope), got %d", got)
	}
	for i, d := range under.docs {
		if d["request_id"] != strconv.Itoa(i) {
			t.Fatalf("se conservan los primeros en orden; en %d hay %v", i, d["request_id"])
		}
	}
}

func TestDeepSanitizePointerCycleDoesNotStackOverflow(t *testing.T) {
	type node struct {
		Name string
		Next *node
	}
	n := &node{Name: "n"}
	n.Next = n // ciclo

	// Sin límite de profundidad esto revienta con stack overflow fatal.
	got := deepSanitize(n)

	cur := got
	found := false
	for i := 0; i < 200; i++ {
		m, ok := cur.(map[string]any)
		if !ok {
			s, isStr := cur.(string)
			found = isStr && s == "<max depth>"
			break
		}
		next, ok := m["Next"]
		if !ok {
			break
		}
		cur = next
	}
	if !found {
		t.Fatalf("el ciclo no quedó cortado por el límite de profundidad: %#v", got)
	}
}

func TestDeepSanitizeCopiesMaps(t *testing.T) {
	m := map[string]any{"k": "v", "n": 1}
	out, ok := deepSanitize(m).(map[string]any)
	if !ok {
		t.Fatalf("deepSanitize debía devolver map[string]any, got %T", out)
	}

	// El llamador sigue usando su mapa: la copia no debe verse afectada.
	m["k"] = "mutado"
	m["nueva"] = true

	if out["k"] != "v" || out["n"] != 1 || len(out) != 2 {
		t.Fatalf("deepSanitize devolvió una referencia al mapa del llamador: %v", out)
	}

	// Claves no-string se convierten con fmt.Sprint.
	got := deepSanitize(map[int]string{1: "a"})
	if m2, ok := got.(map[string]any); !ok || m2["1"] != "a" {
		t.Fatalf("clave de mapa no convertida a string: %#v", got)
	}
}

// Sin el arreglo, el worker sanitizaba el mapa del llamador mientras este ya
// seguía mutándolo: con -race esto reporta "concurrent map iteration and map write".
func TestAsyncPayloadMapMutationDoesNotRace(t *testing.T) {
	under := &recordingDriver{delay: time.Millisecond}
	s := NewAsyncService(1, 8, under)

	for i := 0; i < 50; i++ {
		m := map[string]any{"k": i}
		if err := s.CreateLog(context.Background(), Logger{Level: "INFO", Payload: []any{m}}); err != nil {
			t.Fatal(err)
		}
		m["k"] = i + 1000 // mutación inmediata tras encolar
		m["otro"] = i
	}
	s.Close()

	if got := under.count(); got != 50 {
		t.Fatalf("se esperaban 50 logs, got %d", got)
	}
}

func TestBatchAsyncPayloadMapMutationDoesNotRace(t *testing.T) {
	under := &recordingDriver{}
	bd := NewBatchDriver(under, 4, 2*time.Millisecond)
	s := NewAsyncService(1, 8, bd)

	for i := 0; i < 50; i++ {
		m := map[string]any{"k": i}
		if err := s.CreateLog(context.Background(), Logger{Level: "INFO", Payload: []any{m}}); err != nil {
			t.Fatal(err)
		}
		m["k"] = i + 1000
		m["otro"] = i
	}
	s.Close()

	if got := under.count(); got != 50 {
		t.Fatalf("se esperaban 50 logs, got %d", got)
	}
}

type panickingDriver struct{}

func (panickingDriver) Create(context.Context, map[string]any) error { panic("driver boom") }
func (panickingDriver) CreateMany(context.Context, []map[string]any) error {
	panic("driver boom many")
}

func TestDriverPanicDoesNotKillWorkerOrOtherDrivers(t *testing.T) {
	okDriver := &recordingDriver{}
	s := NewAsyncService(1, 4, panickingDriver{}, okDriver)

	for i := 0; i < 2; i++ {
		if err := s.CreateLog(context.Background(), Logger{Level: "INFO"}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if got := okDriver.count(); got != 2 {
		t.Fatalf("el driver sano no recibió los logs tras el panic del otro: got %d", got)
	}

	// En modo sync el panic tampoco debe propagarse al llamador.
	syncOk := &recordingDriver{}
	syncS := NewService(panickingDriver{}, syncOk)
	if err := syncS.CreateLog(context.Background(), Logger{Level: "INFO"}); err != nil {
		t.Fatal(err)
	}
	if got := syncOk.count(); got != 1 {
		t.Fatalf("el driver sano no recibió el log en modo sync: got %d", got)
	}
}

// stuckDriver se queda colgado hasta que su contexto se agota (respeta ctx).
type stuckDriver struct{ recordingDriver }

func (d *stuckDriver) Create(ctx context.Context, doc map[string]any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Minute):
		return d.recordingDriver.Create(ctx, doc)
	}
}

func TestStuckDriverTimeoutLetsCloseReturn(t *testing.T) {
	s := NewAsyncService(1, 4, &stuckDriver{})
	s.driverTimeout = 50 * time.Millisecond

	if err := s.CreateLog(context.Background(), Logger{Level: "INFO"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close no retornó: el driver atascado bloqueó al worker indefinidamente")
	}
}

// Tras una ráfaga la capacidad de la cola no debe provocar una asignación grande
// por cada log posterior (el worker reutiliza el slice en vez de crear otro).
func TestAsyncWorkerDoesNotReallocateBigQueuePerLog(t *testing.T) {
	under := &recordingDriver{}
	s := NewAsyncService(1, 100, under)
	defer s.Close()

	wait := func(n int) {
		deadline := time.Now().Add(5 * time.Second)
		for under.count() < n && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if under.count() < n {
			t.Fatalf("timeout esperando %d logs, hay %d", n, under.count())
		}
	}

	for i := 0; i < 5000; i++ {
		_ = s.CreateLog(context.Background(), Logger{})
	}
	wait(5000)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 1; i <= 200; i++ {
		_ = s.CreateLog(context.Background(), Logger{})
		wait(5000 + i)
	}
	runtime.ReadMemStats(&after)

	if got := after.TotalAlloc - before.TotalAlloc; got > 20<<20 {
		t.Fatalf("se asignaron %d MB para 200 logs sueltos tras una ráfaga", got>>20)
	}
}

// El tope de cola y el timeout salen de la configuración de golog.
func TestServiceAndBatchReadSettings(t *testing.T) {
	st, _ := helper.SettingsFromEnv()
	st.QueueMax, st.DriverTimeout = 3, 2*time.Second
	helper.SetSettings(&st)
	defer helper.ResetSettings()

	s := NewAsyncService(1, 1, &recordingDriver{})
	defer s.Close()
	if s.max != 3 || s.driverTimeout != 2*time.Second {
		t.Fatalf("Service: max=%d timeout=%v", s.max, s.driverTimeout)
	}

	bd := NewBatchDriver(&recordingDriver{}, 2, time.Hour).(*batchDriver)
	defer bd.Close()
	if bd.max != 3 || bd.driverTimeout != 2*time.Second {
		t.Fatalf("batch: max=%d timeout=%v", bd.max, bd.driverTimeout)
	}
}

// Un error en el payload conserva su mensaje (antes se serializaba como {}).
func TestSanitizeKeepsErrorMessage(t *testing.T) {
	if got := deepSanitize(errors.New("boom")); got != "boom" {
		t.Fatalf("got %#v", got)
	}
}
