package database

import (
	"context"
	"log"

	"github.com/Nemutagk/golog/models"
)

// DatabaseDriverAdapter envuelve un adaptador de base de datos provisto por
// quien usa la librería. golog no conoce ni importa ningún driver de DB: solo
// entrega documentos (map[string]any) al adaptador.
//
// El adaptador debe implementar models.Driver (Create) y, opcionalmente,
// models.DriverMany (CreateMany) para insertar por lotes de forma eficiente.
type DatabaseDriverAdapter struct {
	adapter models.Driver
}

var _ models.Driver = (*DatabaseDriverAdapter)(nil)
var _ models.DriverMany = (*DatabaseDriverAdapter)(nil)

func NewDatabaseDriverAdapter(adapter models.Driver) *DatabaseDriverAdapter {
	return &DatabaseDriverAdapter{adapter: adapter}
}

func (d *DatabaseDriverAdapter) Create(ctx context.Context, document map[string]any) error {
	return d.adapter.Create(ctx, document)
}

// CreateMany usa CreateMany del adaptador si lo implementa; si no, inserta uno
// a uno. En el fallback los fallos se registran pero NO se devuelven: un error
// a mitad del lote haría que el batchDriver reintente todo el lote y duplicara
// los documentos ya insertados.
func (d *DatabaseDriverAdapter) CreateMany(ctx context.Context, documents []map[string]any) error {
	if many, ok := d.adapter.(models.DriverMany); ok {
		return many.CreateMany(ctx, documents)
	}
	for _, doc := range documents {
		if err := d.adapter.Create(ctx, doc); err != nil {
			log.Printf("Error inserting log with driver %T: %v", d.adapter, err)
		}
	}
	return nil
}
