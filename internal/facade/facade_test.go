package facade

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fake es un backend programable que cuenta llamadas.
type fake struct {
	calls  atomic.Int64
	status int
	body   string
	err    error
	block  chan struct{}
}

func (f *fake) Do(ctx context.Context, method string, u *url.URL, h http.Header, body []byte) (Response, error) {
	f.calls.Add(1)
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return Response{}, f.err
	}
	st := f.status
	if st == 0 {
		st = 200
	}
	return Response{Status: st, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(f.body)}, nil
}

func serve(t *testing.T, f *Facade, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	f.ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec
}

func TestRoutingByMode(t *testing.T) {
	leg, mod := &fake{body: `{"v":1}`}, &fake{body: `{"v":1}`}
	f := New(Config{Legacy: leg, Modern: mod})
	defer f.Close()

	if rec := serve(t, f, "GET", "/customers/1", ""); rec.Header().Get("X-Served-By") != "legacy" {
		t.Fatal("por defecto, legacy")
	}
	if err := f.SetFlag("customer_get", RouteFlag{Mode: ModeModern}); err != nil {
		t.Fatal(err)
	}
	if rec := serve(t, f, "GET", "/customers/1", ""); rec.Header().Get("X-Served-By") != "modern" || rec.Body.String() != `{"v":1}` {
		t.Fatal("modern")
	}
	// Una ruta no migrada sigue yendo al legado.
	if rec := serve(t, f, "GET", "/reports/monthly", ""); rec.Header().Get("X-Served-By") != "legacy" {
		t.Fatal("unmatched -> legacy")
	}
	if err := f.SetFlag("customer_phone_put", RouteFlag{Mode: ModeShadow}); err == nil {
		t.Fatal("no se puede poner en sombra una escritura")
	}
	if err := f.SetFlag("nope", RouteFlag{Mode: ModeModern}); !errors.Is(err, errUnknownRoute) {
		t.Fatal(err)
	}
}

func TestCanaryPercentIsHonored(t *testing.T) {
	leg, mod := &fake{body: `{}`}, &fake{body: `{}`}
	f := New(Config{Legacy: leg, Modern: mod})
	defer f.Close()
	_ = f.SetFlag("customer_360", RouteFlag{Mode: ModeCanary, Percent: 25})
	for i := 0; i < 4000; i++ {
		serve(t, f, "GET", "/customers/"+itoa(i)+"/360", "")
	}
	share := float64(mod.calls.Load()) / 4000
	if share < 0.22 || share > 0.28 {
		t.Fatalf("25%% pedido, %.1f%% real", share*100)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestShadowDetectsMismatchAndNeverAffectsUser(t *testing.T) {
	leg := &fake{body: `{"name":"ANA","phone":"011 1"}`}
	mod := &fake{body: `{"name":"Ana","phone":"+54 11"}`}
	f := New(Config{Legacy: leg, Modern: mod, Accepted: func(route string) []Rule {
		return []Rule{{Path: "phone", Reason: "test"}}
	}})
	defer f.Close()
	_ = f.SetFlag("customer_get", RouteFlag{Mode: ModeShadow})
	rec := serve(t, f, "GET", "/customers/1", "")
	if rec.Body.String() != leg.body || rec.Header().Get("X-Served-By") != "legacy" {
		t.Fatal("en sombra responde siempre el legado")
	}
	f.WaitShadow()
	rep := f.Report().Routes["customer_get"]
	if rep.Shadowed != 1 || rep.Mismatches != 1 {
		t.Fatalf("%+v", rep)
	}
	recent := f.Report().Recent
	if len(recent) != 1 || len(recent[0].Diffs) != 1 || recent[0].Diffs[0].Path != "name" {
		t.Fatalf("el reporte debería listar solo 'name' (phone está aceptado): %+v", recent)
	}
	if v := f.shadowFields.Value("customer_get", "name"); v != 1 {
		t.Fatal(v)
	}

	// Solo diferencias aceptadas: cuenta como accepted, no como mismatch.
	mod.body = `{"name":"ANA","phone":"+54 11"}`
	serve(t, f, "GET", "/customers/1", "")
	f.WaitShadow()
	rep = f.Report().Routes["customer_get"]
	if rep.Mismatches != 1 || rep.Accepted != 1 {
		t.Fatalf("%+v", rep)
	}

	// El nuevo falla en sombra: el usuario no se entera, se cuenta error.
	mod.err = errors.New("boom")
	if rec := serve(t, f, "GET", "/customers/1", ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	f.WaitShadow()
	if f.Report().Routes["customer_get"].Errors != 1 {
		t.Fatal("error de sombra no contado")
	}
}

func TestShadowStatusMismatch(t *testing.T) {
	f := New(Config{Legacy: &fake{body: `{}`}, Modern: &fake{status: 404, body: `{}`}})
	defer f.Close()
	_ = f.SetFlag("ticket_list", RouteFlag{Mode: ModeShadow})
	serve(t, f, "GET", "/tickets", "")
	f.WaitShadow()
	r := f.Report().Recent
	if len(r) != 1 || r[0].Diffs[0].Path != "$status" {
		t.Fatalf("%+v", r)
	}
}

func TestShadowQueueFullDropsInsteadOfBlocking(t *testing.T) {
	block := make(chan struct{})
	mod := &fake{body: `{}`, block: block}
	f := New(Config{Legacy: &fake{body: `{}`}, Modern: mod, ShadowWorkers: 1, ShadowQueue: 2})
	_ = f.SetFlag("customer_get", RouteFlag{Mode: ModeShadow})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); serve(t, f, "GET", "/customers/1", "") }()
	}
	wg.Wait() // ninguna request de usuario quedó bloqueada por la sombra
	close(block)
	f.Close()
	rep := f.Report().Routes["customer_get"]
	if rep.Dropped == 0 || rep.Dropped+rep.Shadowed != 10 {
		t.Fatalf("%+v", rep)
	}
}

func TestCanaryFallbackOnModernError(t *testing.T) {
	leg := &fake{body: `{"ok":1}`}
	mod := &fake{status: 500, body: `{"error":"internal"}`}
	f := New(Config{Legacy: leg, Modern: mod})
	defer f.Close()
	_ = f.SetFlag("customer_get", RouteFlag{Mode: ModeCanary, Percent: 100})
	rec := serve(t, f, "GET", "/customers/1", "")
	if rec.Body.String() != `{"ok":1}` || rec.Header().Get("X-Served-By") != "legacy-fallback" {
		t.Fatalf("%s %s", rec.Header().Get("X-Served-By"), rec.Body.String())
	}
	if f.Report().Routes["customer_get"].Fallbacks != 1 {
		t.Fatal("fallback no contado")
	}
	// En modo modern (100% decidido) no hay red de seguridad: el 500 llega.
	_ = f.SetFlag("customer_get", RouteFlag{Mode: ModeModern})
	if rec := serve(t, f, "GET", "/customers/1", ""); rec.Code != 500 {
		t.Fatal(rec.Code)
	}
	// Las escrituras nunca hacen fallback (podrían duplicarse).
	_ = f.SetFlag("customer_phone_put", RouteFlag{Mode: ModeCanary, Percent: 100})
	if rec := serve(t, f, "PUT", "/customers/1/phone", `{}`); rec.Code != 500 || leg.calls.Load() != 1 {
		t.Fatalf("code=%d legacy calls=%d", rec.Code, leg.calls.Load())
	}
}

func TestBackendErrorIs502(t *testing.T) {
	f := New(Config{Legacy: &fake{err: errors.New("down")}, Modern: &fake{}})
	defer f.Close()
	if rec := serve(t, f, "GET", "/tickets", ""); rec.Code != 502 {
		t.Fatal(rec.Code)
	}
}

func TestMetricsAndAdmin(t *testing.T) {
	f := New(Config{Legacy: &fake{body: `{}`}, Modern: &fake{body: `{}`}, Admin: true})
	defer f.Close()
	if rec := serve(t, f, "PUT", "/admin/flags/customer_360", `{"mode":"canary","percent":40}`); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := serve(t, f, "PUT", "/admin/flags/customer_360", `{"mode":"canary","percent":400}`); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := serve(t, f, "PUT", "/admin/flags/nope", `{"mode":"modern"}`); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec := serve(t, f, "GET", "/admin/flags", ""); !strings.Contains(rec.Body.String(), `"percent":40`) {
		t.Fatal(rec.Body.String())
	}
	serve(t, f, "GET", "/customers/3/360", "")
	body := serve(t, f, "GET", "/metrics", "").Body.String()
	for _, want := range []string{
		"# TYPE facade_request_duration_seconds histogram",
		`facade_requests_total{route="customer_360",backend=`,
		"# TYPE shadow_mismatch_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q", want)
		}
	}
	noAdmin := New(Config{Legacy: &fake{body: `{}`}, Modern: &fake{}})
	defer noAdmin.Close()
	if rec := serve(t, noAdmin, "GET", "/admin/flags", ""); rec.Header().Get("X-Served-By") != "legacy" {
		t.Fatal("sin Admin, /admin no existe en la fachada (va al legado como cualquier ruta desconocida)")
	}
}
