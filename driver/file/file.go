package file

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Nemutagk/goenvars"
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
		filename = time.Now().Format("2006-01-02") + ".log"
	}
	if err := os.MkdirAll(f.basePath, 0o755); err != nil {
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
	fd, err := os.OpenFile(fileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fd.Close()
	w := bufio.NewWriterSize(fd, 32*1024)

	var buf bytes.Buffer
	for _, document := range docs {
		// lineInt := 0
		// switch v := document["line"].(type) {
		// case int:
		// 	lineInt = v
		// case int32:
		// 	lineInt = int(v)
		// case int64:
		// 	lineInt = int(v)
		// case float64:
		// 	lineInt = int(v)
		// }

		// info := fmt.Sprintf("[%s][%s][%s][%s:%d]\n",
		// 	getTime(), document["request_id"], document["level"], document["file"], lineInt)

		bloqInfo := goenvars.GetEnv("GOLOG_BLOCKING_INFO", "")
		if bloqInfo == "" {
			bloqInfo = "level,env,time,request_id,file"
		}
		bloqInfoParts := strings.Split(bloqInfo, ",")

		info := ""

		for _, bloq := range bloqInfoParts {
			switch strings.TrimSpace(bloq) {
			case "time":
				info += fmt.Sprintf("[%s]", getTime())
			case "request_id":
				info += fmt.Sprintf("[%s]", document["request_id"])
			case "level":
				info += fmt.Sprintf("[%s]", document["level"])
			case "file":
				info += fmt.Sprintf("[%s:%d]", document["file"], document["line"])
			case "app":
				info += fmt.Sprintf("[%s]", goenvars.GetEnv("APP_NAME", "--"))
			case "env":
				info += fmt.Sprintf("[%s]", goenvars.GetEnv("APP_ENV", "--"))
			}
		}
		info += "\n"

		payloadStr := formatPayload(document["payload"])
		buf.WriteString(info)
		buf.WriteString(payloadStr)
		buf.WriteString("\n\n")
	}

	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}
	return w.Flush()
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
