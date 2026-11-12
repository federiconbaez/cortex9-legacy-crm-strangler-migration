package bench

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/facade"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

// Params controla una corrida.
type Params struct {
	Dataset     seed.Config `json:"dataset"`
	Requests    int         `json:"requests_per_endpoint"`
	Warmup      int         `json:"warmup_per_endpoint"`
	Calibrate   int         `json:"query_count_sample"`
	Concurrency int         `json:"concurrency"`
	Seed        uint64      `json:"request_seed"`
}

// Endpoint es un tipo de request del benchmark.
type Endpoint struct {
	Name string
	Path func(r *rand.Rand, cfg seed.Config) string
}

// Endpoints son las tres lecturas medidas.
var Endpoints = []Endpoint{
	{"customer_get", func(r *rand.Rand, c seed.Config) string { return fmt.Sprintf("/customers/%d", 1+r.IntN(c.Customers)) }},
	{"customer_360", func(r *rand.Rand, c seed.Config) string {
		return fmt.Sprintf("/customers/%d/360", 1+r.IntN(c.Customers))
	}},
	{"ticket_list", func(r *rand.Rand, _ seed.Config) string {
		return "/tickets?limit=50&status=" + []string{"open", "pending", "closed", ""}[r.IntN(4)]
	}},
}

// Scenario es una configuración del sistema a medir.
type Scenario struct {
	Name        string
	Description string
	// Prepare deja la base en el estado del escenario y devuelve el handler.
	Prepare func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error)
}

func migrateTo(ctx context.Context, c *migration.Controller, target migration.Phase) error {
	steps := []func() error{
		func() error { return c.Expand(ctx) },
		func() error { return c.EnableDualWrite(ctx) },
		func() error { _, err := c.Backfill(ctx, 200); return err },
		func() error { _, err := c.Verify(ctx); return err },
		func() error { return c.SwitchReads(ctx) },
		func() error { return c.Contract(ctx, 0) },
	}
	for _, s := range steps {
		if c.Phase() >= target {
			return nil
		}
		if err := s(); err != nil {
			return err
		}
	}
	if c.Phase() != target {
		return fmt.Errorf("fase %s, quería %s", c.Phase(), target)
	}
	return nil
}

func facadeAll(env *app.Env, rf facade.RouteFlag) (*facade.Facade, error) {
	f := env.Facade(facade.NewFlags(), nil)
	for _, r := range facade.DefaultRoutes() {
		flag := rf
		if !r.Shadowable && flag.Mode == facade.ModeShadow {
			flag = facade.RouteFlag{Mode: facade.ModeLegacy}
		}
		if err := f.SetFlag(r.Name, flag); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

// Scenarios es la matriz medida. El orden importa solo para el reporte.
var Scenarios = []Scenario{
	{"baseline-legacy", "Monolito legado directo, esquema legado (sin índices). LÍNEA BASE.",
		func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error) {
			return legacy.Handler(), nil, nil
		}},
	{"legacy-con-indices", "Mismo código legado + solo los índices del expand. Aísla el efecto de los índices.",
		func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error) {
			return legacy.Handler(), nil, migration.ExpandIndexes(ctx, env.DB)
		}},
	{"modern-sin-indices", "Código nuevo (sin N+1) sobre el esquema legado sin índices. Aísla el efecto del código.",
		func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error) {
			return env.Modern.Handler(), nil, nil
		}},
	{"fachada-legacy", "Fachada strangler con todo en legacy. Mide el costo de la fachada.",
		func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error) {
			f, err := facadeAll(env, facade.RouteFlag{Mode: facade.ModeLegacy})
			return f, f, err
		}},
	{"fachada-shadow", "Fachada en sombra (responde legacy, compara con modern) con la migración en verified.",
		func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error) {
			if err := migrateTo(ctx, env.Ctl, migration.Verified); err != nil {
				return nil, nil, err
			}
			f, err := facadeAll(env, facade.RouteFlag{Mode: facade.ModeShadow})
			return f, f, err
		}},
	{"final-modern", "Fachada con todo en modern, migración contraída (columna vieja borrada). ESTADO FINAL.",
		func(ctx context.Context, env *app.Env) (http.Handler, *facade.Facade, error) {
			if err := migrateTo(ctx, env.Ctl, migration.Contracted); err != nil {
				return nil, nil, err
			}
			f, err := facadeAll(env, facade.RouteFlag{Mode: facade.ModeModern})
			return f, f, err
		}},
}

// EndpointResult es la medición de un endpoint en un escenario.
type EndpointResult struct {
	Endpoint       string  `json:"endpoint"`
	Requests       int     `json:"requests"`
	Errors         int     `json:"errors"`
	Latency        Latency `json:"latency"`
	ThroughputRPS  float64 `json:"throughput_rps"`
	QueriesPerReq  float64 `json:"queries_per_request"`
	QueriesMin     int64   `json:"queries_min"`
	QueriesMax     int64   `json:"queries_max"`
	WallSeconds    float64 `json:"wall_seconds"`
	ShadowIncluded bool    `json:"queries_include_shadow"`
}

// ScenarioResult agrupa los endpoints de un escenario.
type ScenarioResult struct {
	Name         string                        `json:"name"`
	Description  string                        `json:"description"`
	Phase        string                        `json:"migration_phase"`
	Endpoints    []EndpointResult              `json:"endpoints"`
	ShadowReport map[string]facade.RouteReport `json:"shadow_report,omitempty"`
	Recent       []facade.MismatchRecord       `json:"shadow_recent_mismatches,omitempty"`
}

// Run ejecuta un escenario sobre una copia fresca de template.
func Run(ctx context.Context, sc Scenario, template, workdir string, p Params) (ScenarioResult, error) {
	res := ScenarioResult{Name: sc.Name, Description: sc.Description}
	dbPath := filepath.Join(workdir, sc.Name+".db")
	if err := app.CopyFile(dbPath, template); err != nil {
		return res, err
	}
	env, err := app.Open(ctx, dbPath, nil)
	if err != nil {
		return res, err
	}
	defer env.Close()
	h, f, err := sc.Prepare(ctx, env)
	if err != nil {
		return res, err
	}
	if f != nil {
		defer f.Close()
	}
	res.Phase = env.Ctl.Phase().String()
	srv := httptest.NewServer(h)
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: p.Concurrency * 2}, Timeout: 30 * time.Second}

	for i, ep := range Endpoints {
		r := rand.New(rand.NewPCG(p.Seed, uint64(i)))
		paths := make([]string, p.Warmup+p.Calibrate+p.Requests)
		for k := range paths {
			paths[k] = ep.Path(r, p.Dataset)
		}
		// 1) Calentamiento (descartado).
		for _, path := range paths[:p.Warmup] {
			if _, err := get(client, srv.URL+path); err != nil {
				return res, err
			}
		}
		if f != nil {
			f.WaitShadow()
		}
		// 2) Consultas por request: en serie, para que el contador sea exacto.
		er := EndpointResult{Endpoint: ep.Name, ShadowIncluded: f != nil && sc.Name == "fachada-shadow"}
		var total int64
		er.QueriesMin = -1
		for _, path := range paths[p.Warmup : p.Warmup+p.Calibrate] {
			before := env.DB.Queries()
			if _, err := get(client, srv.URL+path); err != nil {
				return res, err
			}
			if f != nil {
				f.WaitShadow()
			}
			n := env.DB.Queries() - before
			total += n
			if er.QueriesMin < 0 || n < er.QueriesMin {
				er.QueriesMin = n
			}
			if n > er.QueriesMax {
				er.QueriesMax = n
			}
		}
		er.QueriesPerReq = float64(total) / float64(p.Calibrate)
		// 3) Carga concurrente.
		load := paths[p.Warmup+p.Calibrate:]
		lat := make([]time.Duration, len(load))
		errs := make([]bool, len(load))
		var next int
		var mu sync.Mutex
		var wg sync.WaitGroup
		start := time.Now()
		for w := 0; w < p.Concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					mu.Lock()
					k := next
					next++
					mu.Unlock()
					if k >= len(load) {
						return
					}
					t0 := time.Now()
					code, err := get(client, srv.URL+load[k])
					lat[k] = time.Since(t0)
					errs[k] = err != nil || code != http.StatusOK
				}
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		if f != nil {
			f.WaitShadow() // la sombra pendiente no cuenta en la latencia, pero sí se procesa
		}
		for _, e := range errs {
			if e {
				er.Errors++
			}
		}
		er.Requests = len(load)
		er.Latency = Summarize(lat)
		er.WallSeconds = round2(wall.Seconds())
		er.ThroughputRPS = round2(float64(len(load)) / wall.Seconds())
		er.QueriesPerReq = round2(er.QueriesPerReq)
		res.Endpoints = append(res.Endpoints, er)
	}
	if f != nil && sc.Name == "fachada-shadow" {
		rep := f.Report()
		res.ShadowReport = rep.Routes
		res.Recent = rep.Recent
	}
	return res, nil
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func get(c *http.Client, u string) (int, error) {
	resp, err := c.Get(u)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
