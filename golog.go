package golog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Nemutagk/goenvars"

	"github.com/Nemutagk/godb"
	"github.com/Nemutagk/godb/definitions/db"
	"github.com/Nemutagk/golog/driver/file"
	"github.com/Nemutagk/golog/models"
)

var connectManagerOnce sync.Once
var connectionManager godb.ConnectionManager
var logService *models.Service
var closer func()

type config struct {
	drivers   []models.Driver
	async     bool
	workers   int
	queueSize int
	batching  bool
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

func WithAsync(workers, queueSize int) Option {
	return func(c *config) {
		c.async = true
		c.workers = workers
		c.queueSize = queueSize
	}
}

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

		// Batching por ENV
		if goenvars.GetEnvBool("LOG_BATCH_ENABLED", true) {
			cfg.batching = true
		}
		if cfg.batching {
			mSize := goenvars.GetEnvInt("LOG_BATCH_SIZE_MONGO", 50)
			mFlush := time.Duration(goenvars.GetEnvInt("LOG_BATCH_FLUSH_MS_MONGO", 100)) * time.Millisecond
			fSize := goenvars.GetEnvInt("LOG_BATCH_SIZE_FILE", 32)
			fFlush := time.Duration(goenvars.GetEnvInt("LOG_BATCH_FLUSH_MS_FILE", 100)) * time.Millisecond

			for i, d := range cfg.drivers {
				switch d.(type) {
				case models.BulkInserter:
					// Driver que soporta InsertMany (Mongo)
					cfg.drivers[i] = models.NewBatchDriver(d, mSize, mFlush)
				case *file.FileDriver:
					cfg.drivers[i] = models.NewBatchDriver(d, fSize, fFlush)
				default:
					// Otros drivers: usar config de archivo como fallback
					cfg.drivers[i] = models.NewBatchDriver(d, fSize, fFlush)
				}
			}
		}

		if cfg.async {
			logService = models.NewAsyncService(cfg.workers, cfg.queueSize, cfg.drivers...)
		} else {
			logService = models.NewService(cfg.drivers...)
		}
		closer = func() { logService.Close() }
	})
}

func Close() {
	if closer != nil {
		closer()
	}
}

func baseLog(ctx context.Context, level string, args ...interface{}) {
	if connectionManager.Connections == nil || logService == nil {
		panic("Connection manager not initialized. Call Init() first.")
	}

	requestID := ctx.Value("request_id")
	if requestID == nil {
		requestID = "--"
	}

	_, fileName, line, ok := runtime.Caller(2)
	if !ok {
		log.Printf("Caller not resolved")
	}

	_ = logService.CreateLog(ctx, models.Logger{
		Level:     level,
		RequestID: requestID.(string),
		Payload:   args,
		File:      fileName,
		Line:      line,
	})

	header := fmt.Sprintf("[%s][%s][%s][%s:%d]", getTime(), requestID, level, fileName, line)
	fmt.Println(header)
	if len(args) > 0 {
		fmt.Println(formatConsoleArgs(args))
	}
}

func Log(ctx context.Context, args ...interface{}) { baseLog(ctx, "INFO", args...) }
func Error(ctx context.Context, args ...interface{}) {
	frames := captureStack(0) // 0 = sin límite
	stackStr := "STACK TRACE:\n" + strings.Join(frames, "\n")
	args = append(args, stackStr)
	baseLog(ctx, "ERROR", args...)
}
func Debug(ctx context.Context, args ...interface{}) { baseLog(ctx, "DEBUG", args...) }

var (
	tzLoc = func() *time.Location {
		l, err := time.LoadLocation("America/Mexico_City")
		if err != nil {
			return time.Local
		}
		return l
	}()
	bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
)

func getTime() string {
	return time.Now().In(tzLoc).Format("2006-01-02 15:04:05 MST")
}

func formatConsoleArgs(items []interface{}) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 && !isSimpleConsole(items[0]) {
		j, err := json.MarshalIndent(items[0], "", "  ")
		if err != nil {
			return fmt.Sprint(items[0])
		}
		return string(j)
	}
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	defer func() {
		b.Reset()
		bufPool.Put(b)
	}()
	for i, it := range items {
		if i > 0 {
			b.WriteByte(' ')
		}
		if isSimpleConsole(it) {
			fmt.Fprint(b, it)
			continue
		}
		j, err := json.MarshalIndent(it, "", "  ")
		if err != nil {
			fmt.Fprint(b, it)
		} else {
			b.WriteByte('\n')
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

// Captura stack; max=0 sin límite
func captureStack(max int) []string {
	const skip = 3 // runtime.Callers, captureStack, Error
	pcs := make([]uintptr, 64)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	out := make([]string, 0, n)
	for {
		f, more := frames.Next()
		fn := f.Function
		if strings.HasPrefix(fn, "runtime.main") {
			out = append(out, fmt.Sprintf("%s (%s:%d)", fn, f.File, f.Line))
			break
		}
		if strings.HasPrefix(fn, "runtime.goexit") {
			break
		}
		out = append(out, fmt.Sprintf("%s (%s:%d)", fn, f.File, f.Line))
		if max > 0 && len(out) >= max {
			break
		}
		if !more {
			break
		}
	}
	return out
}
