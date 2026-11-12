package bench

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
)

// TestScenariosSmoke corre la matriz completa con el dataset chico y fija lo
// que no depende de la máquina: cero errores, consultas por request (el
// arreglo del N+1 no puede volver sin que esto falle) y cero diferencias en
// la sombra. Los tiempos no se comparan: en CI serían ruido.
func TestScenariosSmoke(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	template := filepath.Join(work, "template.db")
	if err := app.Seed(ctx, template, seed.Small); err != nil {
		t.Fatal(err)
	}
	p := Params{Dataset: seed.Small, Requests: 20, Warmup: 2, Calibrate: 10, Concurrency: 2, Seed: 2026}
	got := map[string]map[string]EndpointResult{}
	for _, sc := range Scenarios {
		r, err := Run(ctx, sc, template, work, p)
		if err != nil {
			t.Fatalf("%s: %v", sc.Name, err)
		}
		got[sc.Name] = map[string]EndpointResult{}
		for _, e := range r.Endpoints {
			if e.Errors != 0 {
				t.Errorf("%s/%s: %d errores", sc.Name, e.Endpoint, e.Errors)
			}
			if e.Requests != p.Requests || e.Latency.P50 <= 0 {
				t.Errorf("%s/%s: medición vacía %+v", sc.Name, e.Endpoint, e)
			}
			got[sc.Name][e.Endpoint] = e
		}
		if sc.Name == "fachada-shadow" {
			for route, rep := range r.ShadowReport {
				if rep.Mismatches != 0 || rep.Errors != 0 || rep.Dropped != 0 {
					t.Errorf("sombra %s: %+v", route, rep)
				}
			}
			if r.ShadowReport["customer_360"].Shadowed == 0 {
				t.Error("la sombra no ejecutó nada")
			}
		}
	}
	// El listado nuevo es una sola consulta; el legado hace N+1.
	if q := got["final-modern"]["ticket_list"].QueriesPerReq; q != 1 {
		t.Errorf("final-modern ticket_list: %.2f consultas/req, quiero 1", q)
	}
	if q := got["baseline-legacy"]["ticket_list"].QueriesPerReq; q < 10 {
		t.Errorf("baseline-legacy ticket_list: %.2f consultas/req, esperaba N+1", q)
	}
	// La vista 360 nueva: cliente + tickets + interacciones = 3 como máximo.
	if e := got["final-modern"]["customer_360"]; e.QueriesMax > 3 {
		t.Errorf("final-modern customer_360: hasta %d consultas/req, quiero <= 3", e.QueriesMax)
	}
	if got["final-modern"]["customer_360"].QueriesPerReq >= got["baseline-legacy"]["customer_360"].QueriesPerReq {
		t.Error("la vista 360 nueva debería hacer menos consultas que la legada")
	}
}
