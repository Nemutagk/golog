package helper

import (
	"testing"
	"time"
)

func TestSettingsFromEnvDefaults(t *testing.T) {
	s, used := SettingsFromEnv()
	if len(used) != 0 {
		t.Skipf("hay variables de golog definidas en el entorno: %v", used)
	}
	if s.Timezone != "America/Mexico_City" || !s.BatchEnabled || s.BatchFileSize != 32 ||
		s.BatchDBSize != 50 || s.QueueMax != 10000 || s.DriverTimeout != 30*time.Second ||
		s.AppName != "--" || s.AppEnv != "--" || len(s.HeaderBlocks) != 5 {
		t.Fatalf("defaults inesperados: %+v", s)
	}
}

func TestSettingsFromEnvValues(t *testing.T) {
	t.Setenv("LOG_QUEUE_MAX", "5")
	t.Setenv("LOG_BATCH_FLUSH_MS_FILE", "250")
	t.Setenv("GOLOG_BLOCKING_INFO", " time , level ,, file")
	t.Setenv("GOLOG_SILENT", "true")
	t.Setenv("LOG_BATCH_ENABLED", "false")
	s, used := SettingsFromEnv()
	if s.QueueMax != 5 || s.BatchFileFlush != 250*time.Millisecond || !s.Silent || s.BatchEnabled {
		t.Fatalf("valores no aplicados: %+v", s)
	}
	if got := s.HeaderBlocks; len(got) != 3 || got[0] != "time" || got[1] != "level" || got[2] != "file" {
		t.Fatalf("bloques: %v", got)
	}
	if len(used) != 5 {
		t.Fatalf("variables usadas: %v", used)
	}
}

// Vacío = ausente (default); inválido = default; ambos sin contar como "usada".
func TestSettingsFromEnvEmptyAndInvalid(t *testing.T) {
	t.Setenv("LOG_TIMEZONE", "")
	t.Setenv("LOG_BATCH_SIZE_FILE", "")
	t.Setenv("LOG_QUEUE_MAX", "abc")
	t.Setenv("LOG_DRIVER_TIMEOUT_MS", "-5")
	s, _ := SettingsFromEnv()
	if s.Timezone != "America/Mexico_City" || s.BatchFileSize != 32 || s.QueueMax != 10000 || s.DriverTimeout != 30*time.Second {
		t.Fatalf("se esperaban los defaults: %+v", s)
	}
}

func TestInvalidTimezoneFallsBackToLocal(t *testing.T) {
	s, _ := SettingsFromEnv()
	s.Timezone = "No/Existe"
	SetSettings(&s)
	defer ResetSettings()
	if Now().Location() != time.Local {
		t.Fatalf("zona inválida debe usar time.Local, got %v", Now().Location())
	}
}

func TestConfigIsBuiltLazilyFromEnvAndResettable(t *testing.T) {
	ResetSettings()
	t.Setenv("LOG_QUEUE_MAX", "11")
	if got := Config().QueueMax; got != 11 {
		t.Fatalf("got %d", got)
	}
	t.Setenv("LOG_QUEUE_MAX", "12")
	if got := Config().QueueMax; got != 11 {
		t.Fatalf("la configuración se construye una sola vez: %d", got)
	}
	ResetSettings()
	if got := Config().QueueMax; got != 12 {
		t.Fatalf("tras ResetSettings se reconstruye: %d", got)
	}
	ResetSettings()
}

// SetSettings guarda una copia: modificar el struct después no afecta a la configuración vigente.
func TestSetSettingsStoresACopy(t *testing.T) {
	s, _ := SettingsFromEnv()
	s.QueueMax = 42
	SetSettings(&s)
	defer ResetSettings()
	s.QueueMax = 99
	if got := Config().QueueMax; got != 42 {
		t.Fatalf("la configuración debe ser una copia: %d", got)
	}
}
