package golog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
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
