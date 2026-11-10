package golog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nemutagk/golog/driver/file"
	"github.com/Nemutagk/golog/helper"
	"github.com/Nemutagk/golog/redact"
)

// captureStdout redirige stdout durante f y devuelve lo escrito.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	f()

	w.Close()
	wg.Wait()
	os.Stdout = orig
	return buf.String()
}

func TestSilencedByDefaultUnderGoTest(t *testing.T) {
	if !IsSilent() {
		t.Fatalf("se esperaba IsSilent()==true al correr bajo `go test`")
	}
	out := captureStdout(t, func() {
		Init() // sin drivers; solo consola
		Log(context.Background(), "no debería verse")
	})
	if out != "" {
		t.Fatalf("salida no vacía bajo silencio: %q", out)
	}
}

func TestErrorDoesNotEmitEXTRAWhenUsingFormatVerbs(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	Init()

	out := captureStdout(t, func() {
		err := errors.New("error de base de datos")
		Error(context.Background(), "error al contar usuarios: %s", err)
	})

	if strings.Contains(out, "%!(EXTRA") {
		t.Fatalf("la salida contiene el artefacto %%!(EXTRA ...):\n%s", out)
	}
	if !strings.Contains(out, "error al contar usuarios: error de base de datos") {
		t.Fatalf("la salida no contiene el mensaje formateado:\n%s", out)
	}
	if !strings.Contains(out, "STACK TRACE:") {
		t.Fatalf("la salida no contiene el STACK TRACE:\n%s", out)
	}
}

func TestSetSilentTogglesOutput(t *testing.T) {
	prev := IsSilent()
	defer SetSilent(prev)

	Init()

	SetSilent(false)
	out := captureStdout(t, func() {
		Log(context.Background(), "visible")
	})
	if !strings.Contains(out, "visible") {
		t.Fatalf("se esperaba ver 'visible' con SetSilent(false): %q", out)
	}

	SetSilent(true)
	out = captureStdout(t, func() {
		Log(context.Background(), "invisible")
	})
	if out != "" {
		t.Fatalf("se esperaba salida vacía con SetSilent(true): %q", out)
	}
}

func TestCallerFileForLogAndError(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	Init()

	cases := []struct {
		name string
		log  func(context.Context, ...interface{})
	}{
		{"Log", Log},
		{"Error", Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				tc.log(context.Background(), "mensaje de prueba")
			})
			if !strings.Contains(out, "golog_test.go:") {
				t.Fatalf("%s no reporta el archivo del test:\n%s", tc.name, out)
			}
			if strings.Contains(out, "golog.go:") {
				t.Fatalf("%s reporta el archivo interno golog.go:\n%s", tc.name, out)
			}
		})
	}
}

func TestLogWithoutInitDoesNotPanic(t *testing.T) {
	prevSilent := IsSilent()
	SetSilent(false)
	defer SetSilent(prevSilent)

	prevService := logService.Load()
	logService.Store(nil)
	defer func() { logService.Store(prevService) }()

	out := captureStdout(t, func() {
		Log(context.Background(), "sin init")
		Warning(context.Background(), "sin init")
		Error(context.Background(), "sin init")
		Debug(context.Background(), "sin init")
	})
	if !strings.Contains(out, "sin init") {
		t.Fatalf("se esperaba salida en consola sin Init:\n%s", out)
	}
}

func TestRequestIDTypesDoNotPanic(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	Init()

	cases := []struct {
		name string
		val  any
		want string
	}{
		{"string", "abc", "[abc]"},
		{"RequestID", RequestID("def"), "[def]"},
		{"other", 42, "[42]"},
		{"nil", nil, "[--]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.val != nil {
				ctx = context.WithValue(ctx, RequestIDKey, tc.val)
			}
			out := captureStdout(t, func() {
				Log(ctx, "msg")
			})
			if !strings.Contains(out, tc.want) {
				t.Fatalf("request_id %v no aparece como %q:\n%s", tc.val, tc.want, out)
			}
		})
	}
}

func TestCountFormatVerbs(t *testing.T) {
	cases := []struct {
		format string
		want   int
	}{
		{"50% completado", 0},
		{"%d", 1},
		{"%%", 0},
		{"%-5.2f", 1},
		{"a %s b %d", 2},
	}
	for _, tc := range cases {
		if got := helper.CountFormatVerbs(tc.format); got != tc.want {
			t.Fatalf("CountFormatVerbs(%q) = %d, want %d", tc.format, got, tc.want)
		}
	}
}

type fakeManyDriver struct{}

func (fakeManyDriver) Create(context.Context, map[string]any) error { return nil }
func (fakeManyDriver) CreateMany(context.Context, []map[string]any) error {
	return nil
}

func TestBatchConfigForDriverTypes(t *testing.T) {
	t.Setenv("LOG_BATCH_SIZE_FILE", "7")
	t.Setenv("LOG_BATCH_FLUSH_MS_FILE", "250")
	t.Setenv("LOG_BATCH_SIZE_MONGO", "9")
	t.Setenv("LOG_BATCH_FLUSH_MS_MONGO", "999")

	st, _ := helper.SettingsFromEnv() // variables de entorno (deprecadas): siguen funcionando

	t.Run("FileDriver usa configuración FILE", func(t *testing.T) {
		size, flush := batchConfigFor(file.NewFileDriver(t.TempDir(), false), &st)
		if size != 7 || flush != 250*time.Millisecond {
			t.Fatalf("got size=%d flush=%v", size, flush)
		}
	})

	t.Run("DriverMany usa configuración MONGO", func(t *testing.T) {
		size, flush := batchConfigFor(fakeManyDriver{}, &st)
		if size != 9 || flush != 999*time.Millisecond {
			t.Fatalf("got size=%d flush=%v", size, flush)
		}
	})
}

// La consola debe emitir TODO el bloque (encabezado, cuerpo y trailer) en una
// sola escritura y con los mismos bytes que antes: "\n"+info+"\n", cuerpo+"\n",
// trailer+"\n". El bloque [time] se deja como comodín porque varía por llamada.
func TestConsoleOutputSingleWriteExactBytes(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	Init()

	ctx := context.WithValue(context.Background(), RequestIDKey, "req-sw")
	out := captureStdout(t, func() {
		baseLogWithTrailer(ctx, "ERROR", "TRAILER-FIJO", 1, "cuerpo")
	})

	pat := regexp.MustCompile(`^\n\[ERROR\]\[[^\]]*\]\[[^\]]+\]\[req-sw\]\[[^\]]*golog_test\.go:\d+\]\ncuerpo\nTRAILER-FIJO\n$`)
	if !pat.MatchString(out) {
		t.Fatalf("salida de consola no coincide con la esperada:\n%q", out)
	}
}

func TestConsoleWithoutArgsAndTrailer(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	Init()

	ctx := context.WithValue(context.Background(), RequestIDKey, "req-noargs")
	out := captureStdout(t, func() {
		baseLogWithTrailer(ctx, "DEBUG", "", 1)
	})

	pat := regexp.MustCompile(`^\n\[DEBUG\]\[[^\]]*\]\[[^\]]+\]\[req-noargs\]\[[^\]]*golog_test\.go:\d+\]\n$`)
	if !pat.MatchString(out) {
		t.Fatalf("salida de consola sin cuerpo/trailer no coincide:\n%q", out)
	}
}

func TestRequestIDNewlineDoesNotForgeConsoleLine(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	Init()

	ctx := context.WithValue(context.Background(), RequestIDKey, "real\nFAKE-LOG-LINE")
	out := captureStdout(t, func() {
		Log(ctx, "mensaje")
	})

	if strings.Contains(out, "\nFAKE-LOG-LINE") {
		t.Fatalf("un request_id con \\n falsificó una línea de consola:\n%q", out)
	}
	if !strings.Contains(out, "[real FAKE-LOG-LINE]") {
		t.Fatalf("el request_id sanitizado no aparece:\n%q", out)
	}
	// "\n" + header + "\n" + cuerpo + "\n": el \n del request_id no debe
	// agregar una línea extra.
	if got := strings.Count(out, "\n"); got != 3 {
		t.Fatalf("se esperaban exactamente 3 saltos de línea, got %d:\n%q", got, out)
	}
}

// Sin el arreglo, Init escribía logService mientras Log lo leía sin
// sincronización: data race. El once se reinicia (y se restaura al final)
// porque en el resto de la suite ya se disparó y el Init de este test no
// volvería a escribir logService, dejando el test vacío.
func TestConcurrentInitAndLogNoRace(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	prevService := logService.Load()
	// Reiniciar el once (asignándolo nuevo: copiarlo sería copiar un lock)
	// para que el Init de este test ejecute su cuerpo; el resto de la suite
	// ya lo disparó y sin esto no escribiría logService ni cubriría la carrera.
	connectManagerOnce = sync.Once{}
	defer func() {
		connectManagerOnce.Do(func() {}) // dejarlo como disparado
		logService.Store(prevService)
	}()

	_ = captureStdout(t, func() {
		initDone := make(chan struct{})
		const numLoggers = 4
		var wg sync.WaitGroup
		wg.Add(numLoggers + 1)
		go func() {
			defer wg.Done()
			// La opción gira DENTRO del once, antes de la escritura de
			// logService: así las lecturas de los loggers cubren de verdad
			// el instante de la escritura sin depender del scheduling. Se
			// gira (en vez de dormir) para que esta goroutine no pase por
			// syscalls/park que el detector de carreras cuenta como
			// sincronización, lo que a veces ocultaría la carrera real.
			Init(func(*config) {
				for i := 0; i < 3e7; i++ {
				}
			})
			close(initDone)
		}()
		for i := 0; i < numLoggers; i++ {
			go func() {
				defer wg.Done()
				// Loguear SIEMPRE antes de comprobar initDone: si Init ya
				// terminó antes de que esta goroutine corra, un select
				// inicial saltaría el Log y no se leería logService.
				for {
					Log(context.Background(), "durante init")
					select {
					case <-initDone:
						return
					default:
					}
				}
			}()
		}
		wg.Wait()
	})
}

// Precedencia: opción > variable de entorno (deprecada) > default.
func TestOptionsOverrideEnvOverrideDefaults(t *testing.T) {
	build := func(opts ...Option) helper.Settings {
		cfg := &config{}
		for _, o := range opts {
			o(cfg)
		}
		st, _ := helper.SettingsFromEnv()
		cfg.apply(&st)
		return st
	}

	if got := build().QueueMax; got != 10000 {
		t.Fatalf("default: %d", got)
	}
	t.Setenv("LOG_QUEUE_MAX", "5")
	if got := build().QueueMax; got != 5 {
		t.Fatalf("entorno: %d", got)
	}
	if got := build(WithQueueMax(7)).QueueMax; got != 7 {
		t.Fatalf("opción debe ganar al entorno: %d", got)
	}
	if got := build(WithQueueMax(-1)).QueueMax; got != 5 {
		t.Fatalf("opción inválida se ignora: %d", got)
	}

	st := build(
		WithTimezone("UTC"), WithRotateLocalTime(true), WithBatching(false),
		WithFileBatch(3, time.Second), WithDatabaseBatch(4, 2*time.Second),
		WithDriverTimeout(time.Minute), WithHeaderBlocks("level", "app"),
		WithFileNameLimit(10), WithAppName("x"), WithAppEnv("y"), WithSilent(true),
	)
	if st.Timezone != "UTC" || !st.RotateLocalTime || st.BatchEnabled ||
		st.BatchFileSize != 3 || st.BatchFileFlush != time.Second ||
		st.BatchDBSize != 4 || st.BatchDBFlush != 2*time.Second ||
		st.DriverTimeout != time.Minute || len(st.HeaderBlocks) != 2 ||
		st.FileNameLimit != 10 || st.AppName != "x" || st.AppEnv != "y" || !st.Silent {
		t.Fatalf("las opciones no se aplicaron: %+v", st)
	}
}

// El aviso de deprecación lista solo las variables definidas y no sobrescritas por opción.
func TestDeprecatedEnvInUse(t *testing.T) {
	_, used := helper.SettingsFromEnv()
	if got := (&config{}).deprecatedEnvInUse(used); len(got) != 0 {
		t.Fatalf("sin variables no debe avisar: %v", got)
	}

	t.Setenv("LOG_QUEUE_MAX", "5")
	t.Setenv("APP_NAME", "x")
	t.Setenv("GOLOG_FILE_LIMIT", "")
	_, used = helper.SettingsFromEnv()

	cfg := &config{}
	WithQueueMax(9)(cfg)
	got := cfg.deprecatedEnvInUse(used)
	if len(got) != 1 || got[0] != "APP_NAME" {
		t.Fatalf("se esperaba solo APP_NAME (LOG_QUEUE_MAX está sobrescrita y GOLOG_FILE_LIMIT vacía): %v", got)
	}
}

// Los bloques del encabezado de consola salen de la configuración.
func TestConsoleHeaderBlocksFromSettings(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	st, _ := helper.SettingsFromEnv()
	st.HeaderBlocks = []string{"app", "level"}
	st.AppName = "mi-app"
	helper.SetSettings(&st)
	defer helper.ResetSettings()

	out := captureStdout(t, func() { Log(context.Background(), "hola") })
	if !strings.Contains(out, "[mi-app][INFO]\n") {
		t.Fatalf("encabezado inesperado: %q", out)
	}
}

func TestConsoleRedaction(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)
	redactor.Store(redact.New(redact.Config{}))
	defer redactor.Store(nil)

	out := captureStdout(t, func() {
		Log(context.Background(), "login password=abc123", map[string]any{"token": "abcdefghijklmnop"}, errors.New("dial postgres://u:pw@h/db"))
	})
	for _, secreto := range []string{"abc123", "abcdefghijklmnop", "pw@h"} {
		if strings.Contains(out, secreto) {
			t.Fatalf("el secreto %q salió en claro:\n%s", secreto, out)
		}
	}
	if !strings.Contains(out, "[password:********]") || !strings.Contains(out, "[token:abcd") {
		t.Fatalf("faltan las máscaras etiquetadas:\n%s", out)
	}
}

// Sin WithRedaction la salida no cambia (compatibilidad).
func TestRedactionIsOffByDefault(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)
	redactor.Store(nil)

	out := captureStdout(t, func() {
		Log(context.Background(), "login password=abc123")
	})
	if !strings.Contains(out, "password=abc123") {
		t.Fatalf("sin enmascarado el texto debe salir intacto:\n%s", out)
	}
}

// Cualquier opción WithRedact* activa el enmascarado y se acumulan.
func TestRedactOptionsBuildConfig(t *testing.T) {
	cfg := &config{}
	if cfg.redact != nil {
		t.Fatal("desactivado por defecto")
	}
	for _, o := range []Option{
		WithRedactKeys("rfc"), WithRedactAllow("token_count"), WithRedactDisable("email"),
		WithRedactPhones(), WithRedactPattern("curp", regexp.MustCompile(`[A-Z]{4}\d{6}`), func(string) string { return "[curp]" }),
	} {
		o(cfg)
	}
	r := cfg.redact
	if r == nil || len(r.Keys) != 1 || len(r.Allow) != 1 || len(r.Disable) != 1 || !r.Phones || len(r.Rules) != 1 {
		t.Fatalf("config inesperada: %+v", r)
	}
	if got := redact.New(*r).String("clave ABCD123456 y a@b.com"); got != "clave [curp] y a@b.com" {
		t.Fatalf("got %q", got)
	}
}

// Un error pasado como argumento muestra su mensaje (antes se imprimía como {}).
func TestErrorArgumentPrintsMessage(t *testing.T) {
	prev := IsSilent()
	SetSilent(false)
	defer SetSilent(prev)

	out := captureStdout(t, func() {
		Log(context.Background(), "fallo", errors.New("boom"))
	})
	if !strings.Contains(out, "boom") || strings.Contains(out, "{}") {
		t.Fatalf("salida inesperada:\n%s", out)
	}
}
