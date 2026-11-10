package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nemutagk/golog/helper"
)

// Ambos modos de rotación en el mismo proceso: por zona configurada (default) y por
// hora local. Kiritimati (UTC+14) casi siempre cae en otro día que la hora local.
func TestResolveFilenameRotationModes(t *testing.T) {
	st, _ := helper.SettingsFromEnv()
	st.Timezone = "Pacific/Kiritimati"
	helper.SetSettings(&st)
	defer helper.ResetSettings()
	d := NewFileDriver(t.TempDir(), true)

	got, err := d.resolveFilename()
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Pacific/Kiritimati")
	if want := time.Now().In(loc).Format("2006-01-02") + ".log"; filepath.Base(got) != want {
		t.Fatalf("por defecto rota por la zona configurada: got %s want %s", filepath.Base(got), want)
	}

	st.RotateLocalTime = true
	helper.SetSettings(&st)
	got, _ = d.resolveFilename()
	if want := time.Now().Format("2006-01-02") + ".log"; filepath.Base(got) != want {
		t.Fatalf("con RotateLocalTime debe usar hora local: got %s want %s", filepath.Base(got), want)
	}
}

// La variable de entorno (deprecada) sigue funcionando como respaldo.
func TestResolveFilenameRotationLocalTimeFromEnv(t *testing.T) {
	t.Setenv("LOG_ROTATE_LOCAL_TIME", "true")
	helper.ResetSettings()
	defer helper.ResetSettings()

	got, err := NewFileDriver(t.TempDir(), true).resolveFilename()
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Now().Format("2006-01-02") + ".log"; filepath.Base(got) != want {
		t.Fatalf("got %s want %s", filepath.Base(got), want)
	}
}

// Los bloques del encabezado del archivo salen de la configuración.
func TestFileHeaderBlocksFromSettings(t *testing.T) {
	st, _ := helper.SettingsFromEnv()
	st.HeaderBlocks = []string{"level", "app", "env"}
	st.AppName, st.AppEnv = "mi-app", "prod"
	helper.SetSettings(&st)
	defer helper.ResetSettings()

	dir := t.TempDir()
	d := NewFileDriver(dir, false)
	if err := d.Create(context.Background(), map[string]any{"level": "INFO", "payload": []any{"hola"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "logs.log"))
	if !strings.HasPrefix(string(b), "[INFO][mi-app][prod]\nhola") {
		t.Fatalf("encabezado inesperado: %q", b)
	}
}

func TestFileAndDirPermissions(t *testing.T) {
	// Directorio inexistente: así se comprueba el modo con el que MkdirAll lo crea.
	base := filepath.Join(t.TempDir(), "sub", "logs")
	d := NewFileDriver(base, false)

	if err := d.Create(context.Background(), map[string]any{"level": "INFO", "request_id": "r", "payload": nil}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(base, "logs.log"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("permisos del archivo: got %o want 640", fi.Mode().Perm())
	}

	di, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o750 {
		t.Fatalf("permisos del directorio: got %o want 750", di.Mode().Perm())
	}
}

func TestTimeBlockUsesCreatedAt(t *testing.T) {
	d := NewFileDriver(t.TempDir(), false)

	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	doc := map[string]any{
		"level":      "INFO",
		"request_id": "r",
		"created_at": at,
		"payload":    []any{"hola"},
	}
	if err := d.Create(context.Background(), doc); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(d.basePath, "logs.log"))
	if err != nil {
		t.Fatal(err)
	}
	want := "[" + helper.FormatTime(at) + "]"
	if !strings.Contains(string(data), want) {
		t.Fatalf("el bloque [time] no corresponde a created_at %s:\n%s", want, data)
	}
}

func TestRequestIDNewlineDoesNotForgeFileLine(t *testing.T) {
	d := NewFileDriver(t.TempDir(), false)

	doc := map[string]any{"level": "INFO", "request_id": "real\nFAKE", "payload": nil}
	if err := d.Create(context.Background(), doc); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(d.basePath, "logs.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "\nFAKE") || strings.Contains(s, "\rFAKE") {
		t.Fatalf("un request_id con \\n falsificó una línea de log:\n%q", s)
	}
	if !strings.Contains(s, "[real FAKE]") {
		t.Fatalf("el request_id sanitizado no aparece:\n%q", s)
	}
}
