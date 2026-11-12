package characterization

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
)

var update = flag.Bool("update", false, "regenera testdata/golden desde el legado")

// step es una request del guion. El guion corre en orden sobre una base
// recién creada, así las escrituras y sus efectos también quedan fijados.
type step struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body,omitempty"`
}

var script = []step{
	{"customer_get_1", "GET", "/customers/1", ""},
	{"customer_get_7", "GET", "/customers/7", ""},
	{"customer_get_missing", "GET", "/customers/999", ""},
	{"customer_get_bad_id", "GET", "/customers/abc", ""},
	{"customer_360_1", "GET", "/customers/1/360", ""},
	{"customer_360_5", "GET", "/customers/5/360", ""},
	{"customer_360_no_tickets", "GET", "/customers/29/360", ""},
	{"customer_360_missing", "GET", "/customers/999/360", ""},
	{"tickets_default", "GET", "/tickets", ""},
	{"tickets_open_5", "GET", "/tickets?status=open&limit=5", ""},
	{"tickets_pending_10", "GET", "/tickets?status=pending&limit=10", ""},
	{"tickets_closed_cap", "GET", "/tickets?status=closed&limit=500", ""},
	{"tickets_negative_limit", "GET", "/tickets?limit=-3", ""},
	{"tickets_bad_limit", "GET", "/tickets?limit=abc", ""},
	{"tickets_unknown_status", "GET", "/tickets?status=nope", ""},
	{"phone_put_valid", "PUT", "/customers/3/phone", `{"phone":"011 4000-1234"}`},
	{"phone_put_after", "GET", "/customers/3", ""},
	{"phone_put_invalid_number", "PUT", "/customers/4/phone", `{"phone":"15-0000-0000"}`},
	{"phone_put_invalid_after", "GET", "/customers/4", ""},
	{"phone_put_missing_customer", "PUT", "/customers/999/phone", `{"phone":"1144445555"}`},
	{"phone_put_bad_json", "PUT", "/customers/3/phone", `{"phone":`},
	{"phone_put_non_string", "PUT", "/customers/3/phone", `{"phone":"1144445555","extra":1}`},
	{"phone_put_no_field", "PUT", "/customers/5/phone", `{}`},
	{"phone_put_no_field_after", "GET", "/customers/5", ""},
}

// golden es lo que se guarda por paso.
type golden struct {
	Request step            `json:"request"`
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body"`
}

func goldenPath(name string) string { return filepath.Join("..", "testdata", "golden", name+".json") }

// canonical re-serializa JSON con claves ordenadas para comparar semántica
// (null sigue siendo distinto de []).
func canonical(t *testing.T, b []byte) string {
	t.Helper()
	if len(bytes.TrimSpace(b)) == 0 {
		return ""
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("JSON inválido %q: %v", b, err)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// newEnv crea una base Small nueva en un directorio temporal.
func newEnv(t *testing.T) *app.Env {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "crm.db")
	if err := app.Seed(ctx, path, seed.Small); err != nil {
		t.Fatal(err)
	}
	env, err := app.Open(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Close() })
	return env
}

func do(t *testing.T, h http.Handler, s step) (int, []byte) {
	t.Helper()
	var body io.Reader
	if s.Body != "" {
		body = strings.NewReader(s.Body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(s.Method, s.Path, body))
	return rec.Code, rec.Body.Bytes()
}

// stepFilter permite excluir pasos (p. ej. escrituras) para algunos objetivos.
type stepFilter func(step) bool

func allSteps(step) bool { return true }

// runScript corre el guion contra h y compara con los golden. ignore lista
// rutas JSON (normalizadas) que no se comparan: diferencias aceptadas.
func runScript(t *testing.T, h http.Handler, keep stepFilter, ignore ...string) {
	t.Helper()
	for _, s := range script {
		if !keep(s) {
			continue
		}
		code, body := do(t, h, s)
		raw, err := os.ReadFile(goldenPath(s.Name))
		if err != nil {
			t.Fatalf("%s: falta golden (corré -update): %v", s.Name, err)
		}
		var g golden
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatalf("%s: golden roto: %v", s.Name, err)
		}
		if code != g.Status {
			t.Errorf("%s: status %d, golden %d", s.Name, code, g.Status)
		}
		got, want := canonical(t, body), canonical(t, g.Body)
		if len(ignore) > 0 {
			got, want = stripPaths(t, got, ignore), stripPaths(t, want, ignore)
		}
		if got != want {
			t.Errorf("%s: cuerpo distinto\n got: %.400s\nwant: %.400s", s.Name, got, want)
		}
	}
}

// stripPaths borra campos "a.b" de un JSON canónico (solo objetos anidados).
func stripPaths(t *testing.T, js string, paths []string) string {
	t.Helper()
	if js == "" {
		return js
	}
	var v any
	_ = json.Unmarshal([]byte(js), &v)
	for _, p := range paths {
		parts := strings.Split(p, ".")
		cur, ok := v.(map[string]any)
		for i := 0; ok && i < len(parts)-1; i++ {
			cur, ok = cur[parts[i]].(map[string]any)
		}
		if ok {
			delete(cur, parts[len(parts)-1])
		}
	}
	out, _ := json.Marshal(v)
	return string(out)
}
