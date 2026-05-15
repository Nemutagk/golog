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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nemutagk/goenvars"

	"github.com/Nemutagk/golog/driver/file"
	"github.com/Nemutagk/golog/helper"
	"github.com/Nemutagk/golog/models"
)

var connectManagerOnce sync.Once
var logService *models.Service
var closer func()

// silenced controla el silenciamiento global (sin consola y sin drivers).
// Se inicializa con detección automática de `go test` y la variable GOLOG_SILENT.
var silenced atomic.Bool

func init() {
	if testing.Testing() {
		silenced.Store(true)
	}
	if goenvars.GetEnvBool("GOLOG_SILENT", false) {
		silenced.Store(true)
	}
}

// SetSilent activa o desactiva el modo silencioso en tiempo de ejecución.
// En modo silencioso no se imprime a consola ni se invoca a los drivers.
func SetSilent(s bool) { silenced.Store(s) }

// IsSilent reporta si el logger está silenciado.
func IsSilent() bool { return silenced.Load() }

type config struct {
	drivers   []models.Driver
	async     bool
	workers   int
	queueSize int
	batching  bool
}

type RequestID string

const RequestIDKey RequestID = "request_id"

type Option func(*config)

func WithDatabaseDriver(driver models.Driver) Option {
	return func(c *config) {
		c.drivers = append(c.drivers, driver)
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

func Init(opts ...Option) {
	connectManagerOnce.Do(func() {
		cfg := &config{}
		for _, o := range opts {
			o(cfg)
		}
		// if len(cfg.drivers) == 0 {
		// 	cfg.drivers = append(cfg.drivers, models.NewMongoDriver(connectionManager))
		// }

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
				case models.DriverMany:
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
	baseLogWithTrailer(ctx, level, "", args...)
}

func baseLogWithTrailer(ctx context.Context, level, trailer string, args ...interface{}) {
	if silenced.Load() {
		return
	}

	requestID := ctx.Value(RequestIDKey)
	if requestID == nil {
		requestID = "--"
	}

	_, fileName, line, ok := runtime.Caller(3)
	if !ok {
		log.Printf("Caller not resolved")
	}

	// Resolver verbos de formato antes de propagar args a drivers / consola,
	// para que cualquier dato adicional (p.ej. el stack trace) no sea
	// interpretado por fmt.Sprintf como argumento extra.
	args = resolveFormatVerbs(args)

	payload := args
	if trailer != "" {
		payload = make([]interface{}, 0, len(args)+1)
		payload = append(payload, args...)
		payload = append(payload, trailer)
	}

	_ = logService.CreateLog(ctx, models.Logger{
		Level:     level,
		RequestID: requestID.(string),
		Payload:   payload,
		File:      fileName,
		Line:      line,
	})

	fileNameLimit := goenvars.GetEnvInt("GOLOG_FILE_LIMIT", 0)
	if fileNameLimit > 0 && len(fileName) > fileNameLimit {
		fileName = "..." + fileName[len(fileName)-fileNameLimit:]
	}

	bloqInfo := goenvars.GetEnv("GOLOG_BLOCKING_INFO", "")
	if bloqInfo == "" {
		bloqInfo = "level,env,time,request_id,file"
	}
	bloqInfoParts := strings.Split(bloqInfo, ",")

	info := "\n"

	for _, bloq := range bloqInfoParts {
		switch strings.TrimSpace(bloq) {
		case "time":
			info += fmt.Sprintf("[%s]", getTime())
		case "request_id":
			info += fmt.Sprintf("[%s]", requestID)
		case "level":
			info += fmt.Sprintf("[%s]", level)
		case "file":
			info += fmt.Sprintf("[%s:%d]", fileName, line)
		case "app":
			info += fmt.Sprintf("[%s]", goenvars.GetEnv("APP_NAME", "--"))
		case "env":
			info += fmt.Sprintf("[%s]", goenvars.GetEnv("APP_ENV", "--"))
		}
	}

	fmt.Println(info)
	if len(args) > 0 {
		fmt.Println(formatConsoleArgs(args))
	}
	if trailer != "" {
		fmt.Println(trailer)
	}
}

// resolveFormatVerbs detecta si args[0] es un format string con verbos y, si
// hay suficientes argumentos para satisfacerlo, los consume con fmt.Sprintf
// dejando un único string como primer elemento. Los argumentos sobrantes
// (más allá de los verbos) se preservan tal cual.
func resolveFormatVerbs(args []interface{}) []interface{} {
	if len(args) == 0 {
		return args
	}
	s, ok := args[0].(string)
	if !ok {
		return args
	}
	verbs := countFormatVerbs(s)
	if verbs == 0 || len(args)-1 < verbs {
		return args
	}
	formatted := fmt.Sprintf(s, args[1:verbs+1]...)
	rest := args[verbs+1:]
	out := make([]interface{}, 0, 1+len(rest))
	out = append(out, formatted)
	out = append(out, rest...)
	return out
}

func Log(ctx context.Context, args ...interface{})     { baseLog(ctx, "INFO", args...) }
func Warning(ctx context.Context, args ...interface{}) { baseLog(ctx, "WARNING", args...) }
func Error(ctx context.Context, args ...interface{}) {
	if silenced.Load() {
		return
	}
	frames := captureStack(0) // 0 = sin límite
	stackStr := "STACK TRACE:\n" + strings.Join(frames, "\n")
	baseLogWithTrailer(ctx, "ERROR", stackStr, args...)
}
func Debug(ctx context.Context, args ...interface{}) { baseLog(ctx, "DEBUG", args...) }

func Sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

func Printf(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}

var (
	tzLoc = func() *time.Location {
		timezone := goenvars.GetEnv("LOG_TIMEZONE", "America/Mexico_City")
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return time.Local
		}
		return loc
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

	if s, ok := items[0].(string); ok {
		verbs := countFormatVerbs(s)
		if verbs > 0 && len(items)-1 >= verbs {
			return fmt.Sprintf(s, items[1:]...)
		}
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

// countFormatVerbs cuenta los "verbs" de fmt en format, ignorando '%%'
func countFormatVerbs(format string) int {
	count := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		// '%%' es un literal '%'
		if i+1 < len(format) && format[i+1] == '%' {
			i++
			continue
		}
		// avanzar hasta encontrar una letra que puede ser el verbo
		j := i + 1
		for j < len(format) && !isAlpha(format[j]) {
			j++
		}
		if j < len(format) {
			count++
			i = j
		}
	}
	return count
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
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

func GenerateRequestID() string {
	return fmt.Sprintf("%s", helper.GetUuidV7())
}
