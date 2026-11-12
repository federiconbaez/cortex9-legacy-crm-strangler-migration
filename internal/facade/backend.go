// Package facade es la fachada del patrón strangler fig: recibe todo el
// tráfico, decide por ruta (feature flags) si atiende el legado o el servicio
// nuevo, ejecuta tráfico en sombra con comparación de respuestas y expone
// métricas.
package facade

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Response es una respuesta ya leída completa (los cuerpos son chicos).
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Backend es algo que atiende requests: el legado o el nuevo.
type Backend interface {
	Do(ctx context.Context, method string, u *url.URL, h http.Header, body []byte) (Response, error)
}

// HandlerBackend llama a un http.Handler en el mismo proceso (tests y bench).
type HandlerBackend struct{ Handler http.Handler }

// Do ejecuta el handler sobre un recorder propio.
func (b HandlerBackend) Do(ctx context.Context, method string, u *url.URL, h http.Header, body []byte) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header = h.Clone()
	rec := &recorder{header: http.Header{}}
	b.Handler.ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return Response{Status: rec.status, Header: rec.header, Body: rec.buf.Bytes()}, nil
}

type recorder struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.buf.Write(p)
}

// HTTPBackend reenvía a otro proceso por HTTP.
type HTTPBackend struct {
	Base   *url.URL
	Client *http.Client
}

// Do reenvía la request a Base conservando path y query.
func (b HTTPBackend) Do(ctx context.Context, method string, u *url.URL, h http.Header, body []byte) (Response, error) {
	target := *b.Base
	target.Path = u.Path
	target.RawQuery = u.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header = h.Clone()
	resp, err := b.Client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Response{}, fmt.Errorf("leyendo respuesta: %w", err)
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
}
