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

	"github.com/Nemutagk/golog/driver/file"
	"github.com/Nemutagk/golog/helper"
	"github.com/Nemutagk/golog/models"
)

var connectManagerOnce sync.Once

// logService se escribe en Init (dentro del sync.Once) y se lee desde
// cualquier goroutine que loguee o llame a Close: atomic.Pointer para que
// Log/Close concurrentes con Init no sean un data race.
var logService atomic.Pointer[models.Service]

// silenced controla el silenciamiento global (sin consola y sin drivers).
// silencedSet distingue un valor fijado explícitamente de tener que aplicar el
// default (silencio bajo `go test` o GOLOG_SILENT/WithSilent).
var (
	silenced    atomic.Bool
	silencedSet atomic.Bool
)

// SetSilent activa o desactiva el modo silencioso en tiempo de ejecución.
// En modo silencioso no se imprime a consola ni se invoca a los drivers.
func SetSilent(s bool) {
	silenced.Store(s)
	silencedSet.Store(true)
}

// IsSilent reporta si el logger está silenciado.
func IsSilent() bool {
	if silencedSet.Load() {
		return silenced.Load()
	}
	return testing.Testing() || helper.Config().Silent
}

type config struct {
	drivers   []models.Driver
	async     bool
	workers   int
	queueSize int

	// Punteros no nulos = la opción fijó el campo y tiene prioridad sobre el entorno.
	silent          *bool
	timezone        *string
	rotateLocalTime *bool
	batchEnabled    *bool
	fileBatchSize   *int
	fileBatchFlush  *time.Duration
	dbBatchSize     *int
	dbBatchFlush    *time.Duration
	queueMax        *int
	driverTimeout   *time.Duration
	headerBlocks    *[]string
	fileNameLimit   *int
	appName         *string
	appEnv          *string
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

// WithAsync activa la cola FIFO sin bloqueo. workers se conserva por compatibilidad
// pero se usa un único worker (varios romperían el orden); queueSize es la capacidad inicial.
func WithAsync(workers, queueSize int) Option {
	return func(c *config) {
		c.async = true
		c.workers = workers
		c.queueSize = queueSize
	}
}

// Opciones de configuración. Tienen prioridad sobre las variables de entorno,
// que están DEPRECADAS y se eliminarán en una versión futura.

// WithSilent activa o desactiva el modo silencioso (equivale a SetSilent al iniciar).
func WithSilent(v bool) Option { return func(c *config) { c.silent = &v } }

// WithTimezone fija la zona horaria de la marca de tiempo y de la rotación diaria.
func WithTimezone(tz string) Option { return func(c *config) { c.timezone = &tz } }

// WithRotateLocalTime hace que la rotación diaria use la hora local del servidor.
func WithRotateLocalTime(v bool) Option { return func(c *config) { c.rotateLocalTime = &v } }

// WithBatching activa o desactiva el batching de los drivers.
func WithBatching(enabled bool) Option { return func(c *config) { c.batchEnabled = &enabled } }

// WithFileBatch fija tamaño e intervalo del batching del driver de archivo.
func WithFileBatch(size int, flush time.Duration) Option {
	return func(c *config) {
		if size > 0 {
			c.fileBatchSize = &size
		}
		if flush > 0 {
			c.fileBatchFlush = &flush
		}
	}
}

// WithDatabaseBatch fija tamaño e intervalo del batching de los drivers de base de datos.
func WithDatabaseBatch(size int, flush time.Duration) Option {
	return func(c *config) {
		if size > 0 {
			c.dbBatchSize = &size
		}
		if flush > 0 {
			c.dbBatchFlush = &flush
		}
	}
}

// WithQueueMax fija el tope de logs pendientes por cola (al llegar se descartan los nuevos).
func WithQueueMax(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.queueMax = &n
		}
	}
}

// WithDriverTimeout fija el timeout por llamada a un driver.
func WithDriverTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.driverTimeout = &d
		}
	}
}

// WithHeaderBlocks fija los campos (y su orden) del encabezado de consola y archivo:
// level, env, time, request_id, app, file.
func WithHeaderBlocks(blocks ...string) Option { return func(c *config) { c.headerBlocks = &blocks } }

// WithFileNameLimit limita la longitud de la ruta del archivo en consola (0 = sin límite).
func WithFileNameLimit(n int) Option { return func(c *config) { c.fileNameLimit = &n } }

// WithAppName fija el valor del bloque "app" del encabezado.
func WithAppName(name string) Option { return func(c *config) { c.appName = &name } }

// WithAppEnv fija el valor del bloque "env" del encabezado.
func WithAppEnv(env string) Option { return func(c *config) { c.appEnv = &env } }

// apply pisa los valores de s (entorno/defaults) con los fijados por opciones.
func (c *config) apply(s *helper.Settings) {
	if c.timezone != nil {
		s.Timezone = *c.timezone
	}
	if c.silent != nil {
		s.Silent = *c.silent
	}
	if c.rotateLocalTime != nil {
		s.RotateLocalTime = *c.rotateLocalTime
	}
	if c.batchEnabled != nil {
		s.BatchEnabled = *c.batchEnabled
	}
	if c.fileBatchSize != nil {
		s.BatchFileSize = *c.fileBatchSize
	}
	if c.fileBatchFlush != nil {
		s.BatchFileFlush = *c.fileBatchFlush
	}
	if c.dbBatchSize != nil {
		s.BatchDBSize = *c.dbBatchSize
	}
	if c.dbBatchFlush != nil {
		s.BatchDBFlush = *c.dbBatchFlush
	}
	if c.queueMax != nil {
		s.QueueMax = *c.queueMax
	}
	if c.driverTimeout != nil {
		s.DriverTimeout = *c.driverTimeout
	}
	if c.headerBlocks != nil {
		s.HeaderBlocks = *c.headerBlocks
	}
	if c.fileNameLimit != nil {
		s.FileNameLimit = *c.fileNameLimit
	}
	if c.appName != nil {
		s.AppName = *c.appName
	}
	if c.appEnv != nil {
		s.AppEnv = *c.appEnv
	}
}

// deprecatedEnvInUse devuelve las variables de entorno definidas cuyo valor se
// está usando (es decir, que ninguna opción sobrescribió).
func (c *config) deprecatedEnvInUse(used []string) []string {
	overridden := map[string]bool{
		"GOLOG_SILENT":             c.silent != nil,
		"LOG_TIMEZONE":             c.timezone != nil,
		"LOG_ROTATE_LOCAL_TIME":    c.rotateLocalTime != nil,
		"LOG_BATCH_ENABLED":        c.batchEnabled != nil,
		"LOG_BATCH_SIZE_FILE":      c.fileBatchSize != nil,
		"LOG_BATCH_FLUSH_MS_FILE":  c.fileBatchFlush != nil,
		"LOG_BATCH_SIZE_MONGO":     c.dbBatchSize != nil,
		"LOG_BATCH_FLUSH_MS_MONGO": c.dbBatchFlush != nil,
		"LOG_QUEUE_MAX":            c.queueMax != nil,
		"LOG_DRIVER_TIMEOUT_MS":    c.driverTimeout != nil,
		"GOLOG_BLOCKING_INFO":      c.headerBlocks != nil,
		"GOLOG_FILE_LIMIT":         c.fileNameLimit != nil,
		"APP_NAME":                 c.appName != nil,
		"APP_ENV":                  c.appEnv != nil,
	}
	var out []string
	for _, name := range used {
		if !overridden[name] {
			out = append(out, name)
		}
	}
	return out
}

func Init(opts ...Option) {
	connectManagerOnce.Do(func() {
		cfg := &config{}
		for _, o := range opts {
			o(cfg)
		}

		// Prioridad: opción > variable de entorno (deprecada) > default.
		settings, used := helper.SettingsFromEnv()
		cfg.apply(&settings)
		helper.SetSettings(&settings)
		if cfg.silent != nil {
			SetSilent(*cfg.silent)
		}
		if env := cfg.deprecatedEnvInUse(used); len(env) > 0 {
			log.Printf("golog: DEPRECATED: las variables de entorno %s serán eliminadas en una versión futura; configura golog con las opciones de golog.Init", strings.Join(env, ", "))
		}

		if settings.BatchEnabled {
			for i, d := range cfg.drivers {
				size, flush := batchConfigFor(d, helper.Config())
				cfg.drivers[i] = models.NewBatchDriver(d, size, flush)
			}
		}

		if cfg.async {
			logService.Store(models.NewAsyncService(cfg.workers, cfg.queueSize, cfg.drivers...))
		} else {
			logService.Store(models.NewService(cfg.drivers...))
		}
	})
}

func Close() {
	if s := logService.Load(); s != nil {
		s.Close()
	}
}

// batchConfigFor elige el tamaño/intervalo de batching según el tipo de driver.
// *file.FileDriver también implementa DriverMany, por eso se evalúa primero.
func batchConfigFor(d models.Driver, s *helper.Settings) (int, time.Duration) {
	switch d.(type) {
	case *file.FileDriver:
		return s.BatchFileSize, s.BatchFileFlush
	case models.DriverMany:
		return s.BatchDBSize, s.BatchDBFlush
	default:
		return s.BatchFileSize, s.BatchFileFlush
	}
}

func baseLog(ctx context.Context, level string, args ...interface{}) {
	baseLogWithTrailer(ctx, level, "", 3, args...)
}

func baseLogWithTrailer(ctx context.Context, level, trailer string, skip int, args ...interface{}) {
	if IsSilent() {
		return
	}

	requestID := ctx.Value(RequestIDKey)
	var requestIDStr string
	switch v := requestID.(type) {
	case nil:
		requestIDStr = "--"
	case string:
		requestIDStr = v
	case RequestID:
		requestIDStr = string(v)
	default:
		requestIDStr = fmt.Sprint(v)
	}

	_, fileName, line, ok := runtime.Caller(skip)
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

	if svc := logService.Load(); svc != nil {
		_ = svc.CreateLog(ctx, models.Logger{
			Level:     level,
			RequestID: requestIDStr,
			Payload:   payload,
			File:      fileName,
			Line:      line,
		})
	}

	cfg := helper.Config()
	fileNameLimit := cfg.FileNameLimit
	if fileNameLimit > 0 && len(fileName) > fileNameLimit {
		fileName = "..." + fileName[len(fileName)-fileNameLimit:]
	}

	info := "\n"

	for _, bloq := range cfg.HeaderBlocks {
		switch bloq {
		case "time":
			info += fmt.Sprintf("[%s]", helper.GetTime())
		case "request_id":
			info += fmt.Sprintf("[%s]", sanitizeRequestID(requestIDStr))
		case "level":
			info += fmt.Sprintf("[%s]", level)
		case "file":
			info += fmt.Sprintf("[%s:%d]", fileName, line)
		case "app":
			info += fmt.Sprintf("[%s]", cfg.AppName)
		case "env":
			info += fmt.Sprintf("[%s]", cfg.AppEnv)
		}
	}

	// Una única escritura: imprimir encabezado, cuerpo y trailer con
	// fmt.Println separados permite que se intercalen con la salida de otras
	// goroutines. Los bytes deben ser idénticos a "\n"+info+"\n", cuerpo+"\n"
	// y trailer+"\n" por separado.
	var out strings.Builder
	out.WriteString(info)
	out.WriteByte('\n')
	if len(args) > 0 {
		out.WriteString(formatConsoleArgs(args))
		out.WriteByte('\n')
	}
	if trailer != "" {
		out.WriteString(trailer)
		out.WriteByte('\n')
	}
	fmt.Print(out.String())
}

// sanitizeRequestID evita que un request_id con \n o \r falsifique líneas de
// log en la consola. Solo aplica al encabezado, nunca al payload.
func sanitizeRequestID(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
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
	verbs := helper.CountFormatVerbs(s)
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
	if IsSilent() {
		return
	}
	frames := captureStack(0) // 0 = sin límite
	stackStr := "STACK TRACE:\n" + strings.Join(frames, "\n")
	baseLogWithTrailer(ctx, "ERROR", stackStr, 2, args...)
}
func Debug(ctx context.Context, args ...interface{}) { baseLog(ctx, "DEBUG", args...) }

func Sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

func Printf(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func formatConsoleArgs(items []interface{}) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 && !helper.IsSimple(items[0]) {
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
		if helper.IsSimple(it) {
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
	return helper.GetUuidV7()
}
