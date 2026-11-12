// Package integration recorre la migración completa con procesos HTTP reales
// (legado, nuevo y fachada en tres servidores locales), como en producción.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/facade"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

type harness struct {
	t      *testing.T
	env    *app.Env
	f      *facade.Facade
	front  *httptest.Server
	legacy *httptest.Server
	client *http.Client
}

func setup(t *testing.T) *harness {
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
	legacySrv := httptest.NewServer(legacy.Handler())
	modernSrv := httptest.NewServer(env.Modern.Handler())
	client := &http.Client{Timeout: 5 * time.Second}
	lu, _ := url.Parse(legacySrv.URL)
	mu, _ := url.Parse(modernSrv.URL)
	f := facade.New(facade.Config{
		Legacy:   facade.HTTPBackend{Base: lu, Client: client},
		Modern:   facade.HTTPBackend{Base: mu, Client: client},
		Accepted: app.AcceptedDiffs(env.Ctl),
		Admin:    true,
	})
	front := httptest.NewServer(f)
	t.Cleanup(func() {
		front.Close()
		f.Close()
		modernSrv.Close()
		legacySrv.Close()
		_ = env.Close()
	})
	return &harness{t: t, env: env, f: f, front: front, legacy: legacySrv, client: client}
}

func (h *harness) call(method, path, body string) (int, string, string) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.front.URL+path, strings.NewReader(body))
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("X-Served-By"), string(b)
}

func (h *harness) flag(route string, rf facade.RouteFlag) {
	h.t.Helper()
	b, _ := json.Marshal(rf)
	if code, _, body := h.call("PUT", "/admin/flags/"+route, string(b)); code != 204 {
		h.t.Fatalf("flag %s: %d %s", route, code, body)
	}
}

// traffic hace una pasada de lecturas sobre todos los clientes y listados.
func (h *harness) traffic() map[string]int {
	served := map[string]int{}
	for id := 1; id <= seed.Small.Customers; id++ {
		for _, p := range []string{fmt.Sprintf("/customers/%d", id), fmt.Sprintf("/customers/%d/360", id)} {
			code, by, body := h.call("GET", p, "")
			if code != 200 || strings.Contains(body, `"error"`) {
				h.t.Fatalf("%s: %d %s", p, code, body)
			}
			served[by]++
		}
	}
	for _, q := range []string{"", "?status=open", "?status=closed&limit=7"} {
		if code, by, _ := h.call("GET", "/tickets"+q, ""); code == 200 {
			served[by]++
		}
	}
	h.f.WaitShadow()
	return served
}

func (h *harness) noMismatches(stage string) {
	h.t.Helper()
	for route, r := range h.f.Report().Routes {
		if r.Mismatches != 0 || r.Errors != 0 || r.Dropped != 0 {
			h.t.Fatalf("%s: %s tiene diferencias: %+v\n%+v", stage, route, r, h.f.Report().Recent)
		}
	}
}

func TestFullStranglerRollout(t *testing.T) {
	ctx := context.Background()
	h := setup(t)
	reads := []string{"customer_get", "customer_360", "ticket_list"}

	// 1. Todo en el legado.
	if s := h.traffic(); s["legacy"] == 0 || s["modern"] != 0 {
		t.Fatalf("etapa 1: %v", s)
	}

	// 2. Sombra en lecturas: responde el legado, se compara con el nuevo.
	for _, r := range reads {
		h.flag(r, facade.RouteFlag{Mode: facade.ModeShadow})
	}
	h.traffic()
	h.noMismatches("sombra")
	if h.f.Report().Routes["customer_360"].Shadowed < float64(seed.Small.Customers) {
		t.Fatal("la sombra no corrió")
	}

	// 3. Expand + escrituras al nuevo (dual-write) + backfill + verify.
	if err := h.env.Ctl.Expand(ctx); err != nil {
		t.Fatal(err)
	}
	h.flag("customer_phone_put", facade.RouteFlag{Mode: facade.ModeModern})
	if err := h.env.Ctl.EnableDualWrite(ctx); err != nil {
		t.Fatal(err)
	}
	if code, by, _ := h.call("PUT", "/customers/2/phone", `{"phone":"+54 11 5555-0001"}`); code != 200 || by != "modern" {
		t.Fatal(code, by)
	}
	if _, err := h.env.Ctl.Backfill(ctx, 8); err != nil {
		t.Fatal(err)
	}
	if v, err := h.env.Ctl.Verify(ctx); err != nil || v.Drift != 0 {
		t.Fatalf("%+v %v", v, err)
	}

	// 4. Canary con sombra para el resto.
	for _, p := range []int{10, 50} {
		for _, r := range reads {
			h.flag(r, facade.RouteFlag{Mode: facade.ModeCanary, Percent: p, Shadow: true})
		}
		s := h.traffic()
		if s["modern"] == 0 || s["legacy"] == 0 {
			t.Fatalf("canary %d%%: %v", p, s)
		}
		h.noMismatches(fmt.Sprintf("canary %d", p))
	}

	// 5. Cambio de lecturas: aparecen diferencias ACEPTADAS (teléfono), cero mismatches.
	if err := h.env.Ctl.SwitchReads(ctx); err != nil {
		t.Fatal(err)
	}
	h.traffic()
	h.noMismatches("reads_switched")
	if h.f.Report().Routes["customer_get"].Accepted == 0 {
		t.Fatal("esperaba diferencias aceptadas de teléfono tras el switch")
	}

	// 6. Contract bloqueado mientras haya rutas que lleguen al legado.
	if err := h.env.Ctl.Contract(ctx, h.f.LegacyReaders()); err == nil {
		t.Fatal("contract debería fallar con rutas en canary")
	}
	for _, r := range reads {
		h.flag(r, facade.RouteFlag{Mode: facade.ModeModern})
	}
	if n := h.f.LegacyReaders(); n != 0 {
		t.Fatal(n)
	}
	if err := h.env.Ctl.Contract(ctx, h.f.LegacyReaders()); err != nil {
		t.Fatal(err)
	}
	if h.env.Ctl.Phase() != migration.Contracted {
		t.Fatal(h.env.Ctl.Phase())
	}
	if s := h.traffic(); s["legacy"] != 0 {
		t.Fatalf("tras el contract nada debería llegar al legado: %v", s)
	}
	_, _, body := h.call("GET", "/customers/2", "")
	if !strings.Contains(body, `"phone":"+54 11 5555-0001"`) {
		t.Fatalf("el dato escrito en dual-write sobrevive al contract: %s", body)
	}

	// 7. El legado, llamado directo, ya no puede leer: y como traga errores,
	// responde 200 "not found". Es la razón de exigir LegacyReaders()==0.
	resp, err := h.client.Get(h.legacy.URL + "/customers/2")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "not found") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}

	// 8. Métricas visibles por la fachada.
	_, _, metrics := h.call("GET", "/metrics", "")
	for _, want := range []string{`shadow_requests_total{route="customer_360"}`, `facade_request_duration_seconds_bucket{route="customer_360",backend="modern",le="+Inf"}`, `shadow_accepted_diff_total{route="customer_get"}`} {
		if !strings.Contains(metrics, want) {
			t.Errorf("falta %s en /metrics", want)
		}
	}
}
