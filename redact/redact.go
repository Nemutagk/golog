// Package redact enmascara datos sensibles (contraseñas, tokens, correos, tarjetas...)
// en los argumentos de un log antes de que lleguen a la consola o a los drivers.
package redact

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Rule detecta un patrón en un texto y lo enmascara. Mask recibe el texto que
// coincidió y devuelve el reemplazo (si devuelve el mismo texto, no hay cambio).
type Rule struct {
	Name string
	Re   *regexp.Regexp
	Mask func(match string) string
}

// Config define qué se enmascara. El cero valor usa solo las reglas por defecto.
type Config struct {
	Keys    []string // palabras extra que vuelven sensible una clave (mapas, campos, JSON)
	Allow   []string // claves exactas que NO se tocan (p. ej. token_count)
	Rules   []Rule   // patrones propios, además de los por defecto
	Disable []string // nombres de reglas por defecto a desactivar (p. ej. "email")
	Phones  bool     // activa el detector de teléfonos (propenso a falsos positivos)
}

// Redactor aplica la configuración. Es seguro para uso concurrente.
type Redactor struct {
	keys  []keyKind
	allow map[string]bool
	rules []Rule
}

type keyKind struct{ word, kind string }

const maxDepth = 32

func New(c Config) *Redactor {
	r := &Redactor{allow: map[string]bool{}}
	r.keys = append(r.keys, defaultKeys...)
	for _, k := range c.Keys {
		r.keys = append(r.keys, keyKind{normalize(k), "secret"})
	}
	for _, k := range c.Allow {
		r.allow[normalize(k)] = true
	}
	off := map[string]bool{}
	for _, n := range c.Disable {
		off[n] = true
	}
	for _, rule := range defaultRules {
		if !off[rule.Name] {
			r.rules = append(r.rules, rule)
		}
	}
	if c.Phones && !off["phone"] {
		r.rules = append(r.rules, phoneRule)
	}
	r.rules = append(r.rules, c.Rules...)
	return r
}

// Values devuelve una copia de args con los datos sensibles enmascarados.
func (r *Redactor) Values(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = r.safeWalk(a)
	}
	return out
}

// safeWalk evita que un panic (un Error() con receptor nil, una regla propia mal
// escrita) tumbe a quien loguea: el valor se reemplaza por un marcador.
func (r *Redactor) safeWalk(a any) (out any) {
	defer func() {
		if rec := recover(); rec != nil {
			out = fmt.Sprintf("<unserializable:%T>", a)
		}
	}()
	out, _ = r.walk(a, 0)
	return out
}

// String enmascara los patrones que aparezcan dentro de un texto. Todas las
// reglas se evalúan sobre el texto ORIGINAL y los solapes se resuelven a favor de
// la regla que va primero: así una regla no vuelve a coincidir con la salida de otra.
func (r *Redactor) String(s string) string {
	type span struct {
		from, to int
		repl     string
	}
	var spans []span
	for _, rule := range r.rules {
		for _, loc := range rule.Re.FindAllStringIndex(s, -1) {
			repl := rule.Mask(s[loc[0]:loc[1]])
			if repl == s[loc[0]:loc[1]] {
				continue // la regla decidió no tocarlo (p. ej. no pasa Luhn)
			}
			overlaps := false
			for _, sp := range spans {
				if loc[0] < sp.to && loc[1] > sp.from {
					overlaps = true
					break
				}
			}
			if !overlaps {
				spans = append(spans, span{loc[0], loc[1], repl})
			}
		}
	}
	if len(spans) == 0 {
		return s
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(s[last:sp.from])
		b.WriteString(sp.repl)
		last = sp.to
	}
	b.WriteString(s[last:])
	return b.String()
}

// keyMask devuelve el valor enmascarado si la clave es sensible.
func (r *Redactor) keyMask(key string, v any) (any, bool) {
	n := normalize(key)
	if r.allow[n] {
		return nil, false
	}
	for _, k := range r.keys {
		if strings.Contains(n, k.word) {
			return maskKind(k.kind, fmt.Sprint(v)), true
		}
	}
	return nil, false
}

// walk copia v enmascarando lo sensible. changed indica si algo se enmascaró
// (un struct sin cambios se devuelve intacto, conservando su formato).
func (r *Redactor) walk(v any, depth int) (any, bool) {
	if v == nil {
		return nil, false
	}
	if depth > maxDepth {
		return "<max depth>", true
	}
	if err, ok := v.(error); ok {
		s := r.String(err.Error())
		return s, true
	}
	switch v.(type) {
	case json.Marshaler, encoding.TextMarshaler: // time.Time, etc.: no se inspeccionan
		return v, false
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		s := rv.String()
		o := r.String(s)
		return o, o != s
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil, false
		}
		return r.walk(rv.Elem().Interface(), depth+1)
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return v, false // []byte: opaco
		}
		out := make([]any, rv.Len())
		changed := false
		for i := range out {
			var c bool
			out[i], c = r.walk(rv.Index(i).Interface(), depth+1)
			changed = changed || c
		}
		return out, changed
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		changed := false
		for it := rv.MapRange(); it.Next(); {
			key := fmt.Sprint(it.Key().Interface())
			val := it.Value().Interface()
			if m, ok := r.keyMask(key, val); ok {
				out[key], changed = m, true
				continue
			}
			var c bool
			out[key], c = r.walk(val, depth+1)
			changed = changed || c
		}
		return out, changed
	case reflect.Struct:
		return r.walkStruct(v, rv, depth)
	}
	return v, false
}

func (r *Redactor) walkStruct(v any, rv reflect.Value, depth int) (any, bool) {
	out := make(map[string]any, rv.NumField())
	changed := false
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		if f.PkgPath != "" { // no exportado: reflect no puede leerlo
			continue
		}
		name := f.Name
		if j := strings.Split(f.Tag.Get("json"), ",")[0]; j != "" && j != "-" {
			name = j
		}
		val := rv.Field(i).Interface()
		if tag := f.Tag.Get("golog"); tag != "" {
			changed = true
			if tag == "-" {
				continue // el campo se omite
			}
			out[name] = maskKind(tag, fmt.Sprint(val))
			continue
		}
		if m, ok := r.keyMask(name, val); ok {
			out[name], changed = m, true
			continue
		}
		var c bool
		out[name], c = r.walk(val, depth+1)
		changed = changed || c
	}
	if !changed {
		return v, false
	}
	return out, true
}

func normalize(k string) string {
	k = strings.ToLower(k)
	return strings.NewReplacer("_", "", "-", "", " ", "", ".", "").Replace(k)
}
