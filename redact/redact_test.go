package redact

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type User struct {
	Name     string
	Email    string `golog:"email"`
	Password string `json:"password"`
	Note     string `golog:"-"`
	Born     time.Time
}

type Plain struct {
	Name string
	Age  int
}

func TestRedact(t *testing.T) {
	r := New(Config{Allow: []string{"token_count"}})

	cases := map[string]struct{ in, want string }{
		"jwt":    {"auth eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk x", "auth [token:eyJh…] x"},
		"bearer": {"Authorization: Bearer abcdef123456789", "Authorization: Bearer [token:abcd…]"},
		"aws":    {"key AKIAIOSFODNN7EXAMPLE ok", "key [token:AKIA…] ok"},
		"url":    {"dial postgres://admin:s3cr3t@db:5432/x", "dial postgres://admin:[password:********]@db:5432/x"},
		"pair":   {"login password=abc123 user=ana", "login password=[password:********] user=ana"},
		"pair2":  {`{"token": "xyz"}`, `{"token": "[token:********]"}`},
		"card":   {"pago 4111 1111 1111 1111 ok", "pago [card:**** **** **** 1111] ok"},
		"nocard": {"id 1234 5678 9012 3456 ok", "id 1234 5678 9012 3456 ok"},
		"email":  {"escribe a juan.perez@dominio.com ya", "escribe a [email:j***@dominio.com] ya"},
	}
	for name, c := range cases {
		if got := r.String(c.in); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", name, got, c.want)
		}
	}

	// mapas, structs, errores, time.Time intacto
	out := r.Values([]any{
		map[string]any{"password": "p4ss", "token_count": 3, "api_key": "ghp_abcdefghijkl", "n": 1},
		User{"ana", "ana@x.com", "p4ss", "interno", time.Unix(0, 0).UTC()},
		Plain{"bob", 7},
		errors.New("dial postgres://u:pw@h/db"),
	})
	m := out[0].(map[string]any)
	if m["password"] != "[password:********]" || m["token_count"] != 3 || m["api_key"] != "[token:ghp_…]" || m["n"] != 1 {
		t.Errorf("mapa: %v", m)
	}
	u := out[1].(map[string]any)
	if u["Email"] != "[email:a***@x.com]" || u["password"] != "[password:********]" || u["Name"] != "ana" {
		t.Errorf("struct: %v", u)
	}
	if _, ok := u["Note"]; ok {
		t.Errorf("campo golog:\"-\" debe omitirse")
	}
	if _, ok := u["Born"].(time.Time); !ok {
		t.Errorf("time.Time debe quedar intacto: %T", u["Born"])
	}
	if p, ok := out[2].(Plain); !ok || p.Name != "bob" {
		t.Errorf("struct sin secretos debe devolverse intacto: %T %v", out[2], out[2])
	}
	if s, _ := out[3].(string); !strings.Contains(s, "[password:") || strings.Contains(s, "pw@") {
		t.Errorf("error: %v", out[3])
	}
}

func TestDisableAndPhones(t *testing.T) {
	r := New(Config{Disable: []string{"email"}})
	if got := r.String("a@b.com"); got != "a@b.com" {
		t.Errorf("email desactivado: %q", got)
	}
	r = New(Config{Phones: true})
	if got := r.String("llama al +52 55 1234 5678"); !strings.Contains(got, "[phone:***5678]") {
		t.Errorf("teléfono: %q", got)
	}
}

type panicErr struct{}

func (*panicErr) Error() string { panic("boom") }

func TestPanicInValueDoesNotEscape(t *testing.T) {
	var e *panicErr
	out := New(Config{}).Values([]any{error(e), "ok"})
	if s, _ := out[0].(string); !strings.HasPrefix(s, "<unserializable:") || out[1] != "ok" {
		t.Fatalf("got %v", out)
	}
}
