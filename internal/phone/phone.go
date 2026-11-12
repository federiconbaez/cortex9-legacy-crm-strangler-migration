// Package phone normaliza teléfonos argentinos de texto libre a E.164.
//
// No es libphonenumber: cubre los formatos que aparecen en los datos del caso
// (+54 11 ..., 011 ..., (11) ..., 11xxxxxxxx, +54 9 11 ...). Lo que no
// reconoce lo devuelve como inválido para que la migración lo ponga en
// cuarentena en vez de perderlo.
package phone

import (
	"errors"
	"strings"
)

// ErrInvalid indica que el texto no se puede normalizar con confianza.
var ErrInvalid = errors.New("phone: no normalizable")

// Normalize devuelve el número en formato E.164 (+54...).
func Normalize(raw string) (string, error) {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "549") && len(d) == 13:
		return "+" + d, nil
	case strings.HasPrefix(d, "54") && len(d) == 12:
		return "+" + d, nil
	case strings.HasPrefix(d, "0") && len(d) == 11:
		return "+54" + d[1:], nil
	case len(d) == 10 && !strings.HasPrefix(d, "15") && !strings.HasPrefix(d, "0"):
		return "+54" + d, nil
	}
	return "", ErrInvalid
}

// Display formatea un E.164 argentino para mostrar: "+54 11 1234-5678".
// Si no tiene la forma esperada lo devuelve tal cual.
func Display(e164 string) string {
	d := strings.TrimPrefix(e164, "+")
	mobile := ""
	if strings.HasPrefix(d, "549") && len(d) == 13 {
		mobile, d = "9 ", "54"+d[3:]
	}
	if !strings.HasPrefix(d, "54") || len(d) != 12 {
		return e164
	}
	n := d[2:]
	return "+54 " + mobile + n[:2] + " " + n[2:6] + "-" + n[6:]
}
