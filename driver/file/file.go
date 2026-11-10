package file

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Nemutagk/golog/helper"
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
	filename := "logs.log"
	if f.rotateDaily {
		now := helper.Now()
		if helper.Config().RotateLocalTime {
			now = time.Now()
		}
		filename = now.Format("2006-01-02") + ".log"
	}
	if err := os.MkdirAll(f.basePath, 0o750); err != nil {
		return "", err
	}
	return filepath.Join(f.basePath, filename), nil
}

func (f *FileDriver) Create(ctx context.Context, document map[string]any) error {
	return f.CreateMany(ctx, []map[string]any{document})
}

// CreateMany usado por batching
func (f *FileDriver) CreateMany(ctx context.Context, docs []map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fileName, err := f.resolveFilename()
	if err != nil {
		return err
	}
	fd, err := os.OpenFile(fileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer fd.Close()

	cfg := helper.Config()
	var buf bytes.Buffer
	for _, document := range docs {
		info := ""

		for _, bloq := range cfg.HeaderBlocks {
			switch bloq {
			case "time":
				// La hora del log es su created_at, no la hora en que se escribe
				// el archivo (el batching puede retrasarla).
				at := time.Now()
				if t, ok := document["created_at"].(time.Time); ok {
					at = t
				}
				info += fmt.Sprintf("[%s]", helper.FormatTime(at))
			case "request_id":
				info += sanitizeHeaderField(fmt.Sprintf("[%s]", document["request_id"]))
			case "level":
				info += fmt.Sprintf("[%s]", document["level"])
			case "file":
				info += fmt.Sprintf("[%s:%d]", document["file"], document["line"])
			case "app":
				info += fmt.Sprintf("[%s]", cfg.AppName)
			case "env":
				info += fmt.Sprintf("[%s]", cfg.AppEnv)
			}
		}
		info += "\n"

		payloadStr := formatPayload(document["payload"])
		buf.WriteString(info)
		buf.WriteString(payloadStr)
		buf.WriteString("\n\n")
	}

	if _, err := fd.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func (f *FileDriver) String() string {
	return fmt.Sprintf("FileDriver(path=%s rotateDaily=%v)", f.basePath, f.rotateDaily)
}

func formatPayload(p any) string {
	if p == nil {
		return ""
	}
	switch v := p.(type) {
	case []interface{}:
		return joinItems(v)
	default:
		if helper.IsSimple(v) {
			return fmt.Sprint(v)
		}
		j, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(j)
	}
}

// sanitizeHeaderField evita que un request_id con \n o \r falsifique líneas
// de log en el archivo. Solo aplica al encabezado, nunca al payload.
func sanitizeHeaderField(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func joinItems(items []interface{}) string {
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
	var b bytes.Buffer
	for i, it := range items {
		if i > 0 {
			b.WriteByte(' ')
		}
		if helper.IsSimple(it) {
			b.WriteString(fmt.Sprint(it))
			continue
		}
		j, err := json.MarshalIndent(it, "", "  ")
		if err != nil {
			b.WriteString(fmt.Sprint(it))
		} else {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.Write(j)
		}
	}
	return b.String()
}
