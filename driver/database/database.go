package database

import (
	"github.com/Nemutagk/golog/models"
)

type DatabaseDriverAdapter struct {
	adapter models.Driver
}

func NewDatabaseDriverAdapter(adapter models.Driver) *DatabaseDriverAdapter {
	return &DatabaseDriverAdapter{adapter: adapter}
}

// func (d *DatabaseDriverAdapter) Create(ctx context.Context, document map[string]any) error {
// 	if err := d.adapter.Create(ctx, document); err != nil {
// 		return err
// 	}
// 	return nil
// }

// func (d *DatabaseDriverAdapter) CreateMany(ctx context.Context, documents []map[string]any) error {
// 	manyAdapter, ok := d.adapter.(models.DriverMany)
// 	if !ok {
// 		// Fallback a Create si no soporta CreateMany
// 		for _, doc := range documents {
// 			if err := d.adapter.Create(ctx, doc); err != nil {
// 				return err
// 			}
// 		}
// 		return nil
// 	}

// 	if err := manyAdapter.CreateMany(ctx, documents); err != nil {
// 		return err
// 	}
// 	return nil
// }
