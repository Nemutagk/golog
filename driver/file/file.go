package file

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

type FileDriver struct {
	basePath    string
	rotateDaily bool
	mu          sync.Mutex
}

func NewFileDriver(basePath string, rotateDaily bool) *FileDriver {
	return &FileDriver{
		basePath:    basePath,
		rotateDaily: rotateDaily,
	}
}

func (f *FileDriver) resolveFilename() (string, error) {
	filename := "logs.jsonl"
	if f.rotateDaily {
		filename = time.Now().Format("2006-01-02") + ".log"
	}
	if err := os.MkdirAll(f.basePath, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(f.basePath, filename), nil
}

func (f *FileDriver) Insert(ctx context.Context, document bson.M) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fileName, err := f.resolveFilename()
	if err != nil {
		return err
	}

	fd, err := os.OpenFile(fileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fd.Close()

	// línea
	var lineInt int
	switch v := document["line"].(type) {
	case int:
		lineInt = v
	case int32:
		lineInt = int(v)
	case int64:
		lineInt = int(v)
	case float64:
		lineInt = int(v)
	default:
		lineInt = 0
	}

	info := fmt.Sprintf("[%s][%s][%s:%d]\n", getTime(), document["level"], document["file"], lineInt)

	// formatear payload sin corchetes
	payloadStr := formatPayload(document["payload"])

	var out []byte
	out = append(out, []byte(info)...)
	out = append(out, []byte(payloadStr)...)
	out = append(out, '\n', '\n')

	if _, err := fd.Write(out); err != nil {
		return err
	}

	return nil
}

func (f *FileDriver) String() string {
	return fmt.Sprintf("FileDriver(path=%s rotateDaily=%v)", f.basePath, f.rotateDaily)
}

func getTime() string {
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		loc = time.Local
	}
	return time.Now().In(loc).Format("2006-01-02 15:04:05 MST")
}

// formatea el payload sin envolver todo en []
func formatPayload(p any) string {
	if p == nil {
		return ""
	}

	switch v := p.(type) {
	case []interface{}:
		return joinItems(v)
	default:
		if isSimple(v) {
			return fmt.Sprint(v)
		}
		j, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(j)
	}
}

func isSimple(v any) bool {
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

func joinItems(items []interface{}) string {
	if len(items) == 0 {
		return ""
	}

	// Si solo hay un elemento y es complejo, devolver pretty JSON directo
	if len(items) == 1 && !isSimple(items[0]) {
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

		if isSimple(it) {
			b.WriteString(fmt.Sprint(it))
			continue
		}

		// Objeto complejo: separar con nueva línea para legibilidad
		j, err := json.MarshalIndent(it, "", "  ")
		if err != nil {
			b.WriteString(fmt.Sprint(it))
		} else {
			// Si no es el primer item y el anterior era simple, mejor insertar salto
			if i > 0 {
				b.WriteByte('\n')
			}
			b.Write(j)
		}
	}
	return b.String()
}
