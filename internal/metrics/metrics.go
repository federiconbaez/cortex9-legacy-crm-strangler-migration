// Package metrics implementa a mano el formato de texto de Prometheus
// (exposition format 0.0.4): contadores, histogramas y gauges calculados.
//
// Se escribió sin dependencias para que el repo compile offline y para que el
// formato se pueda leer y explicar en una entrevista. No implementa
// summaries, exemplars ni OpenMetrics.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultBuckets son los límites (en segundos) de los histogramas de latencia.
var DefaultBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type metric interface {
	write(w io.Writer)
	name() string
}

// Registry agrupa métricas y las expone en /metrics.
type Registry struct {
	mu      sync.Mutex
	metrics []metric
}

// NewRegistry crea un registro vacío.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) add(m metric) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.metrics {
		if x.name() == m.name() {
			panic("metrics: nombre duplicado " + m.name())
		}
	}
	r.metrics = append(r.metrics, m)
}

// WriteText escribe todas las métricas, ordenadas por nombre.
func (r *Registry) WriteText(w io.Writer) {
	r.mu.Lock()
	ms := append([]metric(nil), r.metrics...)
	r.mu.Unlock()
	sort.Slice(ms, func(i, j int) bool { return ms[i].name() < ms[j].name() })
	for _, m := range ms {
		m.write(w)
	}
}

// Handler sirve el formato de texto.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.WriteText(w)
	})
}

func escape(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

func labelString(names, values []string, extra ...string) string {
	if len(names) == 0 && len(extra) == 0 {
		return ""
	}
	parts := make([]string, 0, len(names)+1)
	for i, n := range names {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, n, escape(values[i])))
	}
	for i := 0; i+1 < len(extra); i += 2 {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, extra[i], escape(extra[i+1])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

const sep = "\xff"

// CounterVec es un contador con etiquetas.
type CounterVec struct {
	n, help string
	labels  []string
	mu      sync.Mutex
	vals    map[string]float64
}

// NewCounterVec registra un contador.
func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{n: name, help: help, labels: labels, vals: map[string]float64{}}
	r.add(c)
	return c
}

func (c *CounterVec) name() string { return c.n }

// Add suma v (>= 0) a la serie con esas etiquetas.
func (c *CounterVec) Add(v float64, values ...string) {
	if v < 0 {
		panic("metrics: un contador no puede bajar")
	}
	if len(values) != len(c.labels) {
		panic("metrics: cantidad de etiquetas incorrecta en " + c.n)
	}
	c.mu.Lock()
	c.vals[strings.Join(values, sep)] += v
	c.mu.Unlock()
}

// Inc suma 1.
func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

// Value devuelve el valor actual (para tests y reportes).
func (c *CounterVec) Value(values ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vals[strings.Join(values, sep)]
}

func (c *CounterVec) write(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.n, c.help, c.n)
	for _, k := range sortedKeys(c.vals) {
		fmt.Fprintf(w, "%s%s %s\n", c.n, labelString(c.labels, split(k, len(c.labels))), formatFloat(c.vals[k]))
	}
}

// HistogramVec es un histograma con etiquetas.
type HistogramVec struct {
	n, help string
	labels  []string
	buckets []float64
	mu      sync.Mutex
	series  map[string]*hist
}

type hist struct {
	counts []uint64 // no acumulados; se acumulan al exponer
	count  uint64
	sum    float64
}

// NewHistogramVec registra un histograma. buckets debe estar ordenado.
func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	if !sort.Float64sAreSorted(buckets) {
		panic("metrics: buckets desordenados")
	}
	h := &HistogramVec{n: name, help: help, labels: labels, buckets: buckets, series: map[string]*hist{}}
	r.add(h)
	return h
}

func (h *HistogramVec) name() string { return h.n }

// Observe registra una observación.
func (h *HistogramVec) Observe(v float64, values ...string) {
	if len(values) != len(h.labels) {
		panic("metrics: cantidad de etiquetas incorrecta en " + h.n)
	}
	k := strings.Join(values, sep)
	h.mu.Lock()
	s, ok := h.series[k]
	if !ok {
		s = &hist{counts: make([]uint64, len(h.buckets))}
		h.series[k] = s
	}
	i := sort.SearchFloat64s(h.buckets, v) // primer bucket con le >= v
	if i < len(h.buckets) {
		s.counts[i]++
	}
	s.count++
	s.sum += v
	h.mu.Unlock()
}

// Count devuelve cuántas observaciones tiene una serie.
func (h *HistogramVec) Count(values ...string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.series[strings.Join(values, sep)]; ok {
		return s.count
	}
	return 0
}

func (h *HistogramVec) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.n, h.help, h.n)
	keys := make([]string, 0, len(h.series))
	for k := range h.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := h.series[k]
		vals := split(k, len(h.labels))
		var cum uint64
		for i, b := range h.buckets {
			cum += s.counts[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.n, labelString(h.labels, vals, "le", formatFloat(b)), cum)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.n, labelString(h.labels, vals, "le", "+Inf"), s.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.n, labelString(h.labels, vals), formatFloat(s.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.n, labelString(h.labels, vals), s.count)
	}
}

// GaugeFunc es un gauge que se calcula al exponer.
type GaugeFunc struct {
	n, help string
	f       func() float64
}

// NewGaugeFunc registra un gauge calculado.
func (r *Registry) NewGaugeFunc(name, help string, f func() float64) {
	r.add(&GaugeFunc{n: name, help: help, f: f})
}

func (g *GaugeFunc) name() string { return g.n }

func (g *GaugeFunc) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", g.n, g.help, g.n, g.n, formatFloat(g.f()))
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func split(k string, n int) []string {
	if n == 0 {
		return nil
	}
	return strings.SplitN(k, sep, n)
}
