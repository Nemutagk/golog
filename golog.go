package golog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/Nemutagk/godb"
	"github.com/Nemutagk/godb/definitions/db"
	"github.com/Nemutagk/golog/driver/file"
	"github.com/Nemutagk/golog/models"
)

var connectManagerOnce sync.Once
var connectionManager godb.ConnectionManager
var logService *models.Service

func Init(listConnections map[string]db.DbConnection, opts ...Option) {
	connectManagerOnce.Do(func() {
		connectionManager = *godb.InitConnections(listConnections)

		cfg := &config{}
		for _, o := range opts {
			o(cfg)
		}
		if len(cfg.drivers) == 0 {
			cfg.drivers = append(cfg.drivers, models.NewMongoDriver(connectionManager))
		}
		logService = models.NewService(cfg.drivers...)
	})
}

type config struct {
	drivers []models.Driver
}

type Option func(*config)

func WithMongoDriver() Option {
	return func(c *config) {
		c.drivers = append(c.drivers, models.NewMongoDriver(connectionManager))
	}
}

func WithFileDriver(path string, rotateDaily bool) Option {
	return func(c *config) {
		c.drivers = append(c.drivers, file.NewFileDriver(path, rotateDaily))
	}
}

func baseLog(ctx context.Context, level string, args ...interface{}) {
	if connectionManager.Connections == nil || logService == nil {
		panic("Connection manager not initialized. Call Init() first.")
	}

	requestID := ctx.Value("request_id")
	if requestID == nil {
		requestID = "unknown"
	}

	_, fileName, line, ok := runtime.Caller(2)
	if !ok {
		log.Printf("Log llamado desde %s:%d\n", fileName, line)
	}

	logService.CreateLog(ctx, models.Logger{
		Level:     level,
		RequestID: requestID.(string),
		Payload:   args,
		File:      fileName,
		Line:      line,
	})

	header := fmt.Sprintf("[%s][%s][%s:%d]", getTime(), level, fileName, line)
	fmt.Println(header)
	if len(args) > 0 {
		fmt.Println(formatConsoleArgs(args))
	}
}

func Log(ctx context.Context, args ...interface{}) {
	baseLog(ctx, "INFO", args...)
}

func Error(ctx context.Context, args ...interface{}) {
	baseLog(ctx, "ERROR", args...)
}

func Debug(ctx context.Context, args ...interface{}) {
	baseLog(ctx, "DEBUG", args...)
}

func getTime() string {
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		loc = time.Local
	}
	return time.Now().In(loc).Format("2006-01-02 15:04:05 MST")
}

// formatea argumentos para la consola (similar a FileDriver)
func formatConsoleArgs(items []interface{}) string {
	if len(items) == 0 {
		return ""
	}
	// Un solo elemento complejo -> pretty JSON directo
	if len(items) == 1 && !isSimpleConsole(items[0]) {
		j, err := json.MarshalIndent(items[0], "", "  ")
		if err != nil {
			return fmt.Sprint(items[0])
		}
		return string(j)
	}

	var b bytes.Buffer
	for i, it := range items {
		if i > 0 {
			b.WriteByte(' ')
		}
		if isSimpleConsole(it) {
			b.WriteString(fmt.Sprint(it))
			continue
		}
		j, err := json.MarshalIndent(it, "", "  ")
		if err != nil {
			b.WriteString(fmt.Sprint(it))
		} else {
			// Salto de línea antes de bloques complejos si no es el primero
			if i > 0 {
				b.WriteByte('\n')
			}
			b.Write(j)
		}
	}
	return b.String()
}

func isSimpleConsole(v any) bool {
	switch v.(type) {
	case string, fmt.Stringer,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		bool,
		time.Time,
		json.Number:
		return true
	}
	return false
}
