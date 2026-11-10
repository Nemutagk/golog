package helper

import (
	"encoding/json"
	"fmt"
	"time"
	"uuid"
)

func GetUuidV7() string {
	return uuid.NewV7().String()
}

// Now devuelve la hora actual en la zona configurada.
func Now() time.Time {
	return time.Now().In(Config().loc)
}

// FormatTime devuelve t (zona configurada) formateada.
func FormatTime(t time.Time) string {
	return t.In(Config().loc).Format("2006-01-02 15:04:05 MST")
}

// GetTime devuelve la hora actual (zona configurada) formateada.
func GetTime() string {
	return FormatTime(Now())
}

func PrettyPrint(data any, print *bool) string {
	// Convertir el mapa a JSON con sangría
	prettyJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println("Error formatting JSON:", err)
		return ""
	}

	if print == nil {
		print = new(bool)
	}

	if *print {
		fmt.Println(string(prettyJSON))
	}

	return string(prettyJSON)
}

// CountFormatVerbs cuenta los verbos de fmt en format, ignorando '%%'.
// Tras '%' solo acepta flags/ancho/precisión y luego una letra de verbo.
func CountFormatVerbs(format string) int {
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
		j := i + 1
		for j < len(format) && isFormatFlag(format[j]) {
			j++
		}
		if j < len(format) && IsAlpha(format[j]) {
			count++
			i = j
		}
	}
	return count
}

func isFormatFlag(b byte) bool {
	switch b {
	case '+', '-', '#', '.', '*', '[', ']':
		return true
	}
	return b >= '0' && b <= '9'
}

func IsAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// IsSimple reporta si v es un valor primitivo imprimible sin JSON.
func IsSimple(v any) bool {
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
