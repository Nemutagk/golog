package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Nemutagk/golog/helper"
)

type Logger struct {
	Level     string `json:"level" bson:"level"`
	RequestID string `json:"request_id" bson:"request_id"`
	Payload   []any  `json:"payload" bson:"payload"`
	File      string `json:"file" bson:"file"`
	Line      int    `json:"line" bson:"line"`
}

type Driver interface {
	Create(ctx context.Context, document map[string]any) error
}

type DriverMany interface {
	CreateMany(ctx context.Context, documents []map[string]any) error
}

// BulkInserter (opcional para batching real)
type BulkInserter interface {
	Create(ctx context.Context, document map[string]any) error
	CreateMany(ctx context.Context, documents []map[string]any) error
}

// Service (sin batching; batching se hace vía wrapper batchDriver)
//
// En modo async los logs entran a una cola FIFO y un único worker los entrega a
// los drivers: el llamador nunca espera y el orden se conserva. La cola tiene un
// tope (LOG_QUEUE_MAX): al alcanzarlo se descartan los logs nuevos, nunca se
// bloquea ni se crece sin límite.
type Service struct {
	drivers []Driver
	once    sync.Once

	// Async
	async  bool
	mu     sync.Mutex
	queue  []asyncJob
	wake   chan struct{} // aviso al worker (capacidad 1)
	closed bool
	done   chan struct{} // se cierra cuando el worker drenó la cola

	max     int          // tope de logs pendientes (en cola + en proceso)
	pending int          // logs pendientes; protegido por mu
	dropped atomic.Int64 // logs descartados desde el último aviso

	// Timeout por llamada a driver (LOG_DRIVER_TIMEOUT_MS): un driver atascado
	// no debe bloquear para siempre al worker ni a Close().
	driverTimeout time.Duration
}

type asyncJob struct {
	ctx context.Context
	at  time.Time
	log Logger
}

func NewService(drivers ...Driver) *Service {
	return &Service{drivers: drivers, driverTimeout: driverTimeout()}
}

// NewAsyncService crea un servicio con cola. workers se conserva por
// compatibilidad pero se usa un único worker (varios romperían el orden de los
// logs); queueSize es solo la capacidad inicial de la cola, que crece según haga falta.
func NewAsyncService(workers, queueSize int, drivers ...Driver) *Service {
	if queueSize <= 0 {
		queueSize = 100
	}
	s := &Service{
		drivers:       drivers,
		async:         true,
		queue:         make([]asyncJob, 0, queueSize),
		wake:          make(chan struct{}, 1),
		done:          make(chan struct{}),
		max:           queueLimit(),
		driverTimeout: driverTimeout(),
	}
	go s.worker()
	return s
}

func (s *Service) worker() {
	defer close(s.done)
	// Se alternan dos slices: crear uno nuevo en cada vuelta (con el lock tomado)
	// heredaría para siempre la capacidad de la mayor ráfaga.
	var spare []asyncJob
	for {
		s.mu.Lock()
		batch, closed := s.queue, s.closed
		s.queue = spare[:0]
		s.mu.Unlock()

		for _, job := range batch {
			s.process(job.ctx, job.at, job.log)
		}
		clear(batch) // no retener contextos ni payloads ya procesados
		spare = batch[:0]
		s.mu.Lock()
		s.pending -= len(batch)
		s.mu.Unlock()
		reportDropped(&s.dropped, "cola")
		if len(batch) == 0 {
			if closed {
				return
			}
			<-s.wake
		}
	}
}

// process entrega el log ya sanitizado a cada driver. Cada llamada va
// envuelta por separado: un driver que haga panic no debe impedir que los
// demás reciban el log ni tumbar al worker.
func (s *Service) process(ctx context.Context, at time.Time, logData Logger) {
	bsonLog := map[string]any{
		"_id":        helper.GetUuidV7(),
		"created_at": at,
		"level":      logData.Level,
		"request_id": logData.RequestID,
		"payload":    logData.Payload, // ya sanitizado por CreateLog
		"file":       logData.File,
		"line":       logData.Line,
	}
	for _, d := range s.drivers {
		s.callCreate(ctx, d, bsonLog)
	}
}

// callCreate invoca a un driver con timeout y recover.
func (s *Service) callCreate(ctx context.Context, d Driver, doc map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("golog: panic in driver %T: %v", d, r)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, s.driverTimeout)
	defer cancel()
	if err := d.Create(ctx, doc); err != nil {
		log.Printf("Error writing log with driver %T: %v", d, err)
	}
}

const maxSanitizeDepth = 32

// deepSanitize elimina / convierte valores no serializables (funciones, handlers, etc.)
func deepSanitize(v any) any {
	return sanitize(v, 0)
}

// sanitize es deepSanitize con control de profundidad: un ciclo de punteros
// (n.Next = n) provocaría un stack overflow fatal sin ese límite.
func sanitize(v any, depth int) any {
	if v == nil {
		return nil
	}
	if depth > maxSanitizeDepth {
		return "<max depth>"
	}

	if e, ok := v.(error); ok {
		return e.Error() // sin esto, *errors.errorString se desreferencia a un struct vacío y se imprime {}
	}

	rv := reflect.ValueOf(v)
	rt := rv.Type()

	switch rt.Kind() {
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return fmt.Sprintf("<unserializable:%T>", v)
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return sanitize(rv.Elem().Interface(), depth+1)
	case reflect.Slice, reflect.Array:
		n := rv.Len()
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = sanitize(rv.Index(i).Interface(), depth+1)
		}
		return out
	case reflect.Map:
		// Copiar en vez de devolver la referencia del llamador: la sanitización
		// puede correr en una goroutine distinta de la que sigue mutando el
		// original (data race en modo async).
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = sanitize(iter.Value().Interface(), depth+1)
		}
		return out
	case reflect.Struct:
		// Intentar marshal directo; si falla, convertir a string
		if _, err := json.Marshal(v); err != nil {
			m := map[string]any{}
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if f.PkgPath != "" { // no exportado
					continue
				}
				val := rv.Field(i).Interface()
				m[f.Name] = sanitize(val, depth+1)
			}
			return m
		}
		return v
	default:
		// Primitivos u otros tipos soportados
		if _, err := json.Marshal(map[string]any{"v": v}); err != nil {
			return fmt.Sprintf("<unserializable:%T>", v)
		}
		return v
	}
}

// sanitizePayload construye un slice nuevo con el payload sanitizado. Se llama
// en el llamador, antes de encolar: el worker async no debe compartir
// referencias (slices ni mapas) con quien sigue mutando su payload.
func sanitizePayload(payload []any) []any {
	if payload == nil {
		return nil
	}
	out := make([]any, 0, len(payload))
	for _, v := range payload {
		out = append(out, sanitizeValue(v))
	}
	return out
}

// sanitizeValue es deepSanitize con recover: un panic durante la sanitización
// no debe tumbar ni al llamador ni al worker async.
func sanitizeValue(v any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("golog: panic sanitizing payload value %T: %v", v, r)
			out = fmt.Sprintf("<unserializable:%T>", v)
		}
	}()
	return deepSanitize(v)
}

// CreateLog: sincrónico, o en modo async encola sin bloquear (orden FIFO, no se pierden logs).
func (s *Service) CreateLog(ctx context.Context, logData Logger) error {
	if !s.async {
		// Modo síncrono, sin cambios: sanitiza y procesa en la goroutine del llamador.
		logData.Payload = sanitizePayload(logData.Payload)
		s.process(ctx, time.Now(), logData)
		return nil
	}

	// Comprobar cerrado/lleno ANTES de sanitizar: la sanitización usa
	// reflexión y es costosa, y el llamador no debe pagarla por un log que
	// se va a descartar. Si hay hueco se suelta el lock para sanitizar y se
	// repiten las comprobaciones: el estado pudo cambiar mientras tanto.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("log service is closed")
	}
	if s.pending >= s.max {
		s.mu.Unlock()
		s.dropped.Add(1)
		return nil
	}
	s.mu.Unlock()

	// Sanitizar aquí y sobre un slice nuevo: el llamador sigue ejecutando y
	// mutando su payload después de encolar; si el worker sanitizara después
	// compartiría la referencia (data race).
	logData.Payload = sanitizePayload(logData.Payload)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("log service is closed")
	}
	if s.pending >= s.max {
		s.mu.Unlock()
		s.dropped.Add(1)
		return nil
	}
	s.pending++
	// WithoutCancel: si la petición termina antes de que el worker procese el log,
	// el insert no debe abortarse por el contexto cancelado.
	s.queue = append(s.queue, asyncJob{ctx: context.WithoutCancel(ctx), at: time.Now(), log: logData})
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default: // ya hay un aviso pendiente
	}
	return nil
}

// Close drena la cola (async) y cierra los drivers que lo soporten. Es idempotente.
func (s *Service) Close() {
	s.once.Do(func() {
		if s.async {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			select {
			case s.wake <- struct{}{}:
			default:
			}
			<-s.done
		}
		for _, d := range s.drivers {
			if c, ok := d.(interface{ Close() }); ok {
				c.Close()
			}
		}
	})
}

// queueLimit devuelve el tope de logs pendientes por cola (configuración de golog).
func queueLimit() int { return helper.Config().QueueMax }

// driverTimeout devuelve el timeout por llamada a driver (configuración de golog).
func driverTimeout() time.Duration { return helper.Config().DriverTimeout }

// reportDropped avisa por stderr (log estándar, no golog) de los logs descartados.
func reportDropped(c *atomic.Int64, where string) {
	if n := c.Swap(0); n > 0 {
		log.Printf("golog: %d logs descartados por %s llena (LOG_QUEUE_MAX)", n, where)
	}
}
