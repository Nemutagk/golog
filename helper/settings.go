package helper

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const defaultTimezone = "America/Mexico_City"

// Settings es la configuración vigente de golog. Se fija con SetSettings y se
// lee con Config para que el resto de paquetes no dependa de variables globales
// leídas al importar.
type Settings struct {
	Timezone        string
	RotateLocalTime bool
	BatchEnabled    bool
	BatchFileSize   int
	BatchFileFlush  time.Duration
	BatchDBSize     int
	BatchDBFlush    time.Duration
	QueueMax        int
	DriverTimeout   time.Duration
	HeaderBlocks    []string
	FileNameLimit   int
	AppName         string
	AppEnv          string
	Silent          bool

	loc *time.Location
}

var settings atomic.Pointer[Settings]

// Config devuelve la configuración vigente. Si nadie la fijó, la construye una
// sola vez desde el entorno y la guarda.
func Config() *Settings {
	if s := settings.Load(); s != nil {
		return s
	}
	s, _ := SettingsFromEnv()
	if settings.CompareAndSwap(nil, &s) {
		return &s
	}
	return settings.Load()
}

// SetSettings fija la configuración vigente.
func SetSettings(s *Settings) {
	if s == nil {
		return
	}
	c := *s // copia: si el llamador modifica su struct después, no hay data race
	c.HeaderBlocks = append([]string(nil), s.HeaderBlocks...)
	c.loc = loadLocation(c.Timezone)
	settings.Store(&c)
}

// ResetSettings olvida la configuración fijada; la próxima llamada a Config la
// reconstruye desde el entorno. Pensado para tests.
func ResetSettings() {
	settings.Store(nil)
}

func defaultSettings() Settings {
	return Settings{
		Timezone:        defaultTimezone,
		RotateLocalTime: false,
		BatchEnabled:    true,
		BatchFileSize:   32,
		BatchFileFlush:  100 * time.Millisecond,
		BatchDBSize:     50,
		BatchDBFlush:    100 * time.Millisecond,
		QueueMax:        10000,
		DriverTimeout:   30 * time.Second,
		HeaderBlocks:    []string{"level", "env", "time", "request_id", "file"},
		FileNameLimit:   0,
		AppName:         "--",
		AppEnv:          "--",
		Silent:          false,
	}
}

// SettingsFromEnv construye la configuración desde las variables de entorno y
// devuelve los nombres de las que estaban definidas y no vacías.
func SettingsFromEnv() (Settings, []string) {
	s := defaultSettings()
	var used []string

	s.Silent = envBool("GOLOG_SILENT", s.Silent, &used)
	if v := envString("LOG_TIMEZONE", &used); v != "" {
		s.Timezone = v
	}
	s.RotateLocalTime = envBool("LOG_ROTATE_LOCAL_TIME", s.RotateLocalTime, &used)
	s.BatchEnabled = envBool("LOG_BATCH_ENABLED", s.BatchEnabled, &used)
	s.BatchFileSize = envInt("LOG_BATCH_SIZE_FILE", s.BatchFileSize, &used)
	s.BatchFileFlush = time.Duration(envInt("LOG_BATCH_FLUSH_MS_FILE", int(s.BatchFileFlush/time.Millisecond), &used)) * time.Millisecond
	s.BatchDBSize = envInt("LOG_BATCH_SIZE_MONGO", s.BatchDBSize, &used)
	s.BatchDBFlush = time.Duration(envInt("LOG_BATCH_FLUSH_MS_MONGO", int(s.BatchDBFlush/time.Millisecond), &used)) * time.Millisecond
	if n := envInt("LOG_QUEUE_MAX", s.QueueMax, &used); n > 0 {
		s.QueueMax = n
	}
	if n := envInt("LOG_DRIVER_TIMEOUT_MS", int(s.DriverTimeout/time.Millisecond), &used); n > 0 {
		s.DriverTimeout = time.Duration(n) * time.Millisecond
	}
	if v := envString("GOLOG_BLOCKING_INFO", &used); v != "" {
		s.HeaderBlocks = splitBlocks(v)
	}
	s.FileNameLimit = envInt("GOLOG_FILE_LIMIT", s.FileNameLimit, &used)
	if v := envString("APP_NAME", &used); v != "" {
		s.AppName = v
	}
	if v := envString("APP_ENV", &used); v != "" {
		s.AppEnv = v
	}

	s.loc = loadLocation(s.Timezone)
	return s, used
}

func envString(name string, used *[]string) string {
	v := os.Getenv(name)
	if v == "" {
		return ""
	}
	*used = append(*used, name)
	return v
}

func envBool(name string, def bool, used *[]string) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	*used = append(*used, name)
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(name string, def int, used *[]string) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	*used = append(*used, name)
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitBlocks(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func loadLocation(tz string) *time.Location {
	if tz == "" {
		tz = defaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	return loc
}
