package metrics

import (
	"bufio"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// sampleLine valida la forma de una línea de muestra del formato de texto.
var sampleLine = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{([a-zA-Z_][a-zA-Z0-9_]*="([^"\\]|\\.)*",?)*\})? -?[0-9.e+Inf-]+$`)

func TestTextFormat(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("shadow_mismatch_total", "Diferencias.", "route")
	h := r.NewHistogramVec("req_seconds", "Latencia.", []float64{0.01, 0.1}, "route", "backend")
	r.NewGaugeFunc("db_queries", "Consultas.", func() float64 { return 3 })
	c.Inc(`a"b`)
	c.Add(2, "x")
	h.Observe(0.005, "r", "legacy")
	h.Observe(0.05, "r", "legacy")
	h.Observe(5, "r", "legacy")

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content-type %q", ct)
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "# HELP ") || strings.HasPrefix(l, "# TYPE ") {
			continue
		}
		if !sampleLine.MatchString(l) {
			t.Errorf("línea inválida: %q", l)
		}
	}
	for _, want := range []string{
		`shadow_mismatch_total{route="a\"b"} 1`,
		`shadow_mismatch_total{route="x"} 2`,
		`req_seconds_bucket{route="r",backend="legacy",le="0.01"} 1`,
		`req_seconds_bucket{route="r",backend="legacy",le="0.1"} 2`,
		`req_seconds_bucket{route="r",backend="legacy",le="+Inf"} 3`,
		`req_seconds_count{route="r",backend="legacy"} 3`,
		`req_seconds_sum{route="r",backend="legacy"} 5.055`,
		"# TYPE db_queries gauge\ndb_queries 3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en:\n%s", want, body)
		}
	}
	// Orden por nombre: db_queries < req_seconds < shadow_mismatch_total.
	if !(strings.Index(body, "db_queries") < strings.Index(body, "req_seconds") && strings.Index(body, "req_seconds") < strings.Index(body, "shadow_mismatch_total")) {
		t.Error("las métricas no salen ordenadas por nombre")
	}
}

func TestBucketBoundaryIsInclusive(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogramVec("h", "x", []float64{1, 2})
	h.Observe(1)
	var sb strings.Builder
	r.WriteText(&sb)
	if !strings.Contains(sb.String(), `h_bucket{le="1"} 1`) {
		t.Fatalf("le es inclusivo en Prometheus:\n%s", sb.String())
	}
}

func TestPanicsOnMisuse(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("c", "x", "a")
	for name, f := range map[string]func(){
		"negativo":  func() { c.Add(-1, "v") },
		"etiquetas": func() { c.Inc() },
		"duplicado": func() { r.NewCounterVec("c", "x") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: esperaba panic", name)
				}
			}()
			f()
		}()
	}
}
