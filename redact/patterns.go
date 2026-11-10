package redact

import (
	"regexp"
	"strings"
)

// Palabras (normalizadas) que vuelven sensible una clave, y el tipo de máscara.
var defaultKeys = []keyKind{
	{"password", "password"}, {"passwd", "password"}, {"contrasena", "password"}, {"cvv", "password"}, {"cvc", "password"},
	{"secret", "secret"}, {"privatekey", "secret"},
	{"token", "token"}, {"apikey", "token"}, {"authorization", "token"}, {"cookie", "token"},
	{"cardnumber", "card"}, {"creditcard", "card"},
	{"email", "email"}, {"correo", "email"},
}

// maskKind enmascara v según el tipo, siempre con una etiqueta que dice qué era.
func maskKind(kind, v string) string {
	switch kind {
	case "email":
		return "[email:" + maskEmail(v) + "]"
	case "card":
		return "[card:" + maskCard(v) + "]"
	case "token":
		return "[token:" + head(v, 4) + "]"
	case "phone":
		return "[phone:" + maskTail(v, 4) + "]"
	default: // password, secret y cualquier otro: nunca se muestra parte del valor
		return "[" + kind + ":********]"
	}
}

func head(v string, n int) string {
	if len(v) < 12 { // muy corto: mostrar el inicio revelaría demasiado
		return "********"
	}
	return v[:n] + "…"
}

func maskEmail(v string) string {
	at := strings.LastIndex(v, "@")
	if at < 1 {
		return "********"
	}
	return v[:1] + "***" + v[at:]
}

func maskCard(v string) string {
	d := digits(v)
	if len(d) < 4 {
		return "************"
	}
	return "**** **** **** " + d[len(d)-4:]
}

func maskTail(v string, n int) string {
	d := digits(v)
	if len(d) <= n {
		return "***"
	}
	return "***" + d[len(d)-n:]
}

func digits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// luhn valida el número de una tarjeta (evita enmascarar números cualquiera).
func luhn(d string) bool {
	if len(d) < 13 || len(d) > 19 {
		return false
	}
	sum, alt := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if alt {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

func labelRule(name, kind, expr string) Rule {
	return Rule{name, regexp.MustCompile(expr), func(m string) string { return maskKind(kind, m) }}
}

var phoneRule = Rule{"phone", regexp.MustCompile(`\+?\d[\d\s().-]{8,}\d`), func(m string) string {
	if len(digits(m)) < 10 {
		return m
	}
	return maskKind("phone", m)
}}

var (
	urlCreds = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:)([^@\s/]+)(@)`)
	pair     = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key)(["']?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
	card     = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
)

var defaultRules = []Rule{
	{"private_key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		func(string) string { return "[private_key:********]" }},
	labelRule("jwt", "token", `eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`),
	{"bearer", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`), func(m string) string {
		return "Bearer " + maskKind("token", strings.Fields(m)[1])
	}},
	labelRule("aws_key", "token", `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	{"url_credentials", urlCreds, func(m string) string {
		p := urlCreds.FindStringSubmatch(m)
		return p[1] + "[password:********]" + p[3]
	}},
	{"secret_pair", pair, func(m string) string {
		p := pair.FindStringSubmatch(m)
		v, q := p[3], ""
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			q, v = v[:1], v[1:len(v)-1] // conserva las comillas para no romper el JSON
		}
		kind := "password"
		if w := strings.ToLower(p[1]); w == "token" || strings.HasPrefix(w, "api") {
			kind = "token"
		} else if w == "secret" {
			kind = "secret"
		}
		return p[1] + p[2] + q + maskKind(kind, v) + q
	}},
	{"card", card, func(m string) string {
		if !luhn(digits(m)) {
			return m
		}
		return maskKind("card", m)
	}},
	labelRule("email", "email", `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
}
