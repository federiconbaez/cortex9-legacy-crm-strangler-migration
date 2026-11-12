// Package logging arma un logger estructurado (JSON) con log/slog.
package logging

import (
	"io"
	"log/slog"
	"os"
)

// New devuelve un logger JSON con el campo service fijo. Si w es nil escribe
// en stderr. Con discard=true no escribe nada (útil en el benchmark para no
// medir el costo de la terminal).
func New(service string, w io.Writer, discard bool) *slog.Logger {
	if discard {
		return slog.New(slog.DiscardHandler)
	}
	if w == nil {
		w = os.Stderr
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})).With("service", service)
}
