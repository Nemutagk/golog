package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sync"
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

type dbDriver struct {
	adapter Driver
}

func (m *dbDriver) Create(ctx context.Context, document map[string]any) error {
	err := m.adapter.Create(ctx, document)
	return err
}

func (m *dbDriver) CreateMany(ctx context.Context, documents []map[string]any) error {
	for _, d := range documents {
		if err := m.adapter.Create(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

// Service (sin batching; batching se hace vía wrapper batchDriver)
type Service struct {
	drivers []Driver

	// Async
	async bool
	jobs  chan asyncJob
	wg    sync.WaitGroup
	once  sync.Once
}

type asyncJob struct {
	ctx context.Context
	log Logger
}

func NewService(drivers ...Driver) *Service {
	return &Service{drivers: drivers}
}

func NewAsyncService(workers, queueSize int, drivers ...Driver) *Service {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = workers * 100 // tamaño base
	}
	s := &Service{
		drivers: drivers,
		async:   true,
		jobs:    make(chan asyncJob, queueSize),
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *Service) worker() {
	defer s.wg.Done()
	for job := range s.jobs {
		s.process(job.ctx, job.log)
	}
}

func (s *Service) process(ctx context.Context, logData Logger) {
	sanitized := make([]any, 0, len(logData.Payload))
	for _, v := range logData.Payload {
		sanitized = append(sanitized, deepSanitize(v))
	}

	bsonLog := map[string]any{
		"_id":        helper.GetUuidV7(),
		"created_at": time.Now(),
		"level":      logData.Level,
		"request_id": logData.RequestID,
		"payload":    sanitized,
		"file":       logData.File,
		"line":       logData.Line,
	}
	for _, d := range s.drivers {
		if err := d.Create(ctx, bsonLog); err != nil {
			log.Printf("Error writing log with driver %T: %v", d, err)
		}
	}
}

// deepSanitize elimina / convierte valores no serializables (funciones, handlers, etc.)
func deepSanitize(v any) any {
	if v == nil {
		return nil
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
		return deepSanitize(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		n := rv.Len()
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = deepSanitize(rv.Index(i).Interface())
		}
		return out
	// case reflect.Map:
	// 	out := bson.M{}
	// 	iter := rv.MapRange()
	// 	for iter.Next() {
	// 		k := iter.Key()
	// 		if k.Kind() != reflect.String {
	// 			out[fmt.Sprintf("%v", k.Interface())] = fmt.Sprintf("<non-string-key:%T>", k.Interface())
	// 			continue
	// 		}
	// 		out[k.String()] = deepSanitize(iter.Value().Interface())
	// 	}
	// 	return out
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
				m[f.Name] = deepSanitize(val)
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

// CreateLog sincrónico o encola bloqueando si la cola está llena (no se pierden logs)
func (s *Service) CreateLog(ctx context.Context, logData Logger) error {
	if !s.async {
		s.process(ctx, logData)
		return nil
	}
	// Envío bloqueante: backpressure
	s.jobs <- asyncJob{ctx: ctx, log: logData}
	return nil
}

// Close drena workers (solo en modo async)
func (s *Service) Close() {
	if !s.async {
		// Cerrar batch drivers si existen
		for _, d := range s.drivers {
			if c, ok := d.(interface{ Close() }); ok {
				c.Close()
			}
		}
		return
	}
	s.once.Do(func() {
		close(s.jobs)
		s.wg.Wait()
		for _, d := range s.drivers {
			if c, ok := d.(interface{ Close() }); ok {
				c.Close()
			}
		}
	})
}
