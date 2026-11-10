package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nemutagk/golog/models"
)

type onlyCreate struct {
	docs []map[string]any
	err  error
}

func (o *onlyCreate) Create(_ context.Context, d map[string]any) error {
	if o.err != nil {
		return o.err
	}
	o.docs = append(o.docs, d)
	return nil
}

type withMany struct {
	onlyCreate
	manyCalls int
}

func (w *withMany) CreateMany(_ context.Context, docs []map[string]any) error {
	w.manyCalls++
	w.docs = append(w.docs, docs...)
	return nil
}

func TestCreateManyUsesAdapterCreateManyWhenAvailable(t *testing.T) {
	w := &withMany{}
	a := NewDatabaseDriverAdapter(w)
	if err := a.CreateMany(context.Background(), []map[string]any{{"a": 1}, {"a": 2}}); err != nil {
		t.Fatal(err)
	}
	if w.manyCalls != 1 || len(w.docs) != 2 {
		t.Fatalf("manyCalls=%d docs=%d", w.manyCalls, len(w.docs))
	}
}

func TestCreateManyFallsBackToCreate(t *testing.T) {
	o := &onlyCreate{}
	a := NewDatabaseDriverAdapter(o)
	if err := a.CreateMany(context.Background(), []map[string]any{{"a": 1}, {"a": 2}}); err != nil {
		t.Fatal(err)
	}
	if len(o.docs) != 2 {
		t.Fatalf("docs=%d", len(o.docs))
	}
}

func TestCreatePropagatesError(t *testing.T) {
	boom := errors.New("boom")
	a := NewDatabaseDriverAdapter(&onlyCreate{err: boom})
	if err := a.Create(context.Background(), nil); !errors.Is(err, boom) {
		t.Fatalf("Create: %v", err)
	}
}

// Sin DriverMany, CreateMany no puede devolver error a mitad del lote: el
// batchDriver reintentaría todo el lote y duplicaría lo ya insertado.
func TestCreateManyFallbackSwallowsErrorsToAvoidDuplicates(t *testing.T) {
	// Falla siempre: nada se inserta, pero tampoco se devuelve error.
	a := NewDatabaseDriverAdapter(&onlyCreate{err: errors.New("boom")})
	if err := a.CreateMany(context.Background(), []map[string]any{{"a": 1}, {"a": 2}}); err != nil {
		t.Fatalf("CreateMany en fallback no debe devolver error: %v", err)
	}
}

// flakyOnlyCreate es un adaptador solo-Create que falla en el 3er insert.
type flakyOnlyCreate struct {
	docs []map[string]any
}

func (f *flakyOnlyCreate) Create(_ context.Context, d map[string]any) error {
	if d["n"] == 3 {
		return errors.New("fallo simulado en el 3er insert")
	}
	f.docs = append(f.docs, d)
	return nil
}

// Con el fix, CreateMany en fallback registra el fallo y devuelve nil, así el
// batchDriver no reintenta el lote completo: ningún id queda insertado dos veces.
func TestBatchDoesNotReinsertPartiallyInsertedBatch(t *testing.T) {
	flaky := &flakyOnlyCreate{}
	bd := models.NewBatchDriver(NewDatabaseDriverAdapter(flaky), 3, time.Hour)

	for _, n := range []int{1, 2, 3} {
		if err := bd.Create(context.Background(), map[string]any{"n": n}); err != nil {
			t.Fatal(err)
		}
	}

	// El flush ocurre en la goroutine de fondo; Close lo espera.
	if c, ok := bd.(interface{ Close() }); ok {
		c.Close()
	}

	seen := map[int]int{}
	for _, d := range flaky.docs {
		seen[d["n"].(int)]++
	}
	for _, n := range []int{1, 2} {
		if seen[n] != 1 {
			t.Fatalf("el documento %d quedó insertado %d veces (esperado exactamente 1)", n, seen[n])
		}
	}
	if seen[3] != 0 {
		t.Fatalf("el documento que falló no debía quedar insertado: %v", seen)
	}
}
