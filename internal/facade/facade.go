package facade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/metrics"
)

// Route es una ruta que la fachada conoce por nombre.
type Route struct {
	Name    string
	Pattern string // patrón de net/http (método + path)
	// Shadowable: solo rutas sin efectos (GET) pueden correr en sombra o
	// tener fallback; una escritura duplicada no es "observabilidad".
	Shadowable bool
	// Key devuelve la clave de rollout (estable por cliente).
	Key func(*http.Request) string
}

// DefaultRoutes son las rutas del CRM. Lo que no matchea va al legado.
func DefaultRoutes() []Route {
	byID := func(r *http.Request) string { return r.PathValue("id") }
	return []Route{
		{Name: "customer_get", Pattern: "GET /customers/{id}", Shadowable: true, Key: byID},
		{Name: "customer_360", Pattern: "GET /customers/{id}/360", Shadowable: true, Key: byID},
		{Name: "ticket_list", Pattern: "GET /tickets", Shadowable: true, Key: func(r *http.Request) string {
			if k := r.Header.Get("X-Rollout-Key"); k != "" {
				return k
			}
			return r.URL.RawQuery
		}},
		{Name: "customer_phone_put", Pattern: "PUT /customers/{id}/phone", Key: byID},
	}
}

// Config arma una fachada.
type Config struct {
	Legacy, Modern Backend
	Routes         []Route
	Flags          *Flags
	Log            *slog.Logger
	Registry       *metrics.Registry
	// Accepted devuelve, por ruta, las diferencias conocidas y aceptadas
	// en este momento (puede depender de la fase de migración).
	Accepted func(route string) []Rule
	// ShadowWorkers y ShadowQueue acotan el costo de la sombra: si la cola
	// se llena, se descarta (y se cuenta) en vez de frenar al usuario.
	ShadowWorkers int
	ShadowQueue   int
	ShadowTimeout time.Duration
	// Admin habilita /admin/* (cambiar flags). En producción iría detrás
	// de autenticación; acá es local.
	Admin bool
}

// Facade es el http.Handler de la fachada.
type Facade struct {
	cfg      Config
	mux      *http.ServeMux
	routes   map[string]Route
	jobs     chan shadowJob
	inflight sync.WaitGroup
	workers  sync.WaitGroup
	closeOne sync.Once

	reqTotal     *metrics.CounterVec
	reqDuration  *metrics.HistogramVec
	fallback     *metrics.CounterVec
	shadowTotal  *metrics.CounterVec
	shadowMiss   *metrics.CounterVec
	shadowAccept *metrics.CounterVec
	shadowFields *metrics.CounterVec
	shadowDrop   *metrics.CounterVec
	shadowErr    *metrics.CounterVec
	shadowDur    *metrics.HistogramVec

	repMu  sync.Mutex
	recent []MismatchRecord
}

// MismatchRecord es una entrada del reporte de diferencias.
type MismatchRecord struct {
	Route        string `json:"route"`
	URL          string `json:"url"`
	LegacyStatus int    `json:"legacy_status"`
	ModernStatus int    `json:"modern_status"`
	Diffs        []Diff `json:"diffs"`
}

type shadowJob struct {
	route   string
	method  string
	url     *url.URL
	header  http.Header
	body    []byte
	primary Response
}

const maxRecent = 50

// New crea la fachada y arranca los workers de sombra.
func New(cfg Config) *Facade {
	if cfg.Flags == nil {
		cfg.Flags = NewFlags()
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Registry == nil {
		cfg.Registry = metrics.NewRegistry()
	}
	if cfg.Routes == nil {
		cfg.Routes = DefaultRoutes()
	}
	if cfg.ShadowWorkers <= 0 {
		cfg.ShadowWorkers = 2
	}
	if cfg.ShadowQueue <= 0 {
		cfg.ShadowQueue = 256
	}
	if cfg.ShadowTimeout <= 0 {
		cfg.ShadowTimeout = 2 * time.Second
	}
	reg := cfg.Registry
	f := &Facade{
		cfg:          cfg,
		mux:          http.NewServeMux(),
		routes:       map[string]Route{},
		jobs:         make(chan shadowJob, cfg.ShadowQueue),
		reqTotal:     reg.NewCounterVec("facade_requests_total", "Requests atendidas por la fachada.", "route", "backend", "code"),
		reqDuration:  reg.NewHistogramVec("facade_request_duration_seconds", "Latencia vista por el cliente, por ruta y backend.", metrics.DefaultBuckets, "route", "backend"),
		fallback:     reg.NewCounterVec("facade_fallback_total", "Requests del nuevo que fallaron y se reintentaron en el legado.", "route"),
		shadowTotal:  reg.NewCounterVec("shadow_requests_total", "Requests ejecutadas en sombra contra el nuevo.", "route"),
		shadowMiss:   reg.NewCounterVec("shadow_mismatch_total", "Requests en sombra con al menos una diferencia no aceptada.", "route"),
		shadowAccept: reg.NewCounterVec("shadow_accepted_diff_total", "Requests en sombra con solo diferencias aceptadas.", "route"),
		shadowFields: reg.NewCounterVec("shadow_diff_fields_total", "Diferencias no aceptadas por campo (ruta JSON normalizada).", "route", "path"),
		shadowDrop:   reg.NewCounterVec("shadow_dropped_total", "Requests en sombra descartadas por cola llena.", "route"),
		shadowErr:    reg.NewCounterVec("shadow_errors_total", "Errores al ejecutar o comparar la sombra.", "route"),
		shadowDur:    reg.NewHistogramVec("shadow_duration_seconds", "Latencia del nuevo en sombra.", metrics.DefaultBuckets, "route"),
	}
	for _, rt := range cfg.Routes {
		rt := rt
		f.routes[rt.Name] = rt
		f.mux.HandleFunc(rt.Pattern, func(w http.ResponseWriter, r *http.Request) { f.serveRoute(rt, w, r) })
	}
	f.mux.HandleFunc("GET /metrics", reg.Handler().ServeHTTP)
	f.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	if cfg.Admin {
		f.mux.HandleFunc("GET /admin/flags", f.adminGetFlags)
		f.mux.HandleFunc("PUT /admin/flags/{route}", f.adminSetFlag)
		f.mux.HandleFunc("GET /admin/shadow/report", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.Report())
		})
	}
	// Todo lo que no se migró sigue yendo al legado tal cual.
	f.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.serveRoute(Route{Name: "unmatched"}, w, r)
	})
	for i := 0; i < cfg.ShadowWorkers; i++ {
		f.workers.Add(1)
		go f.shadowWorker()
	}
	return f
}

// ServeHTTP implementa http.Handler.
func (f *Facade) ServeHTTP(w http.ResponseWriter, r *http.Request) { f.mux.ServeHTTP(w, r) }

// Registry expone el registro para agregar métricas externas.
func (f *Facade) Registry() *metrics.Registry { return f.cfg.Registry }

// SetFlag valida y cambia el flag de una ruta.
func (f *Facade) SetFlag(route string, rf RouteFlag) error {
	rt, ok := f.routes[route]
	if !ok {
		return fmt.Errorf("%w: %q", errUnknownRoute, route)
	}
	if !rt.Shadowable && (rf.Mode == ModeShadow || rf.Shadow) {
		return fmt.Errorf("la ruta %q tiene efectos: no se puede ejecutar en sombra", route)
	}
	return f.cfg.Flags.Set(route, rf)
}

// LegacyReaders cuenta rutas que todavía pueden llegar al legado.
func (f *Facade) LegacyReaders() int {
	n := 0
	for name := range f.routes {
		if f.cfg.Flags.Get(name).Mode != ModeModern {
			n++
		}
	}
	return n
}

func (f *Facade) serveRoute(rt Route, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"body"}`, http.StatusBadRequest)
		return
	}
	flag := RouteFlag{Mode: ModeLegacy}
	key := ""
	if rt.Key != nil {
		flag = f.cfg.Flags.Get(rt.Name)
		key = rt.Key(r)
	}
	useModern := flag.UseModern(rt.Name, key)
	backendName, backend := "legacy", f.cfg.Legacy
	if useModern {
		backendName, backend = "modern", f.cfg.Modern
	}
	resp, err := backend.Do(r.Context(), r.Method, r.URL, r.Header, body)
	if useModern && rt.Shadowable && flag.Mode == ModeCanary && (err != nil || resp.Status >= 500) {
		f.fallback.Inc(rt.Name)
		f.cfg.Log.Warn("fallback al legado", "route", rt.Name, "err", errString(err), "status", resp.Status)
		backendName = "legacy-fallback"
		resp, err = f.cfg.Legacy.Do(r.Context(), r.Method, r.URL, r.Header, body)
	}
	if err != nil {
		f.cfg.Log.Error("backend", "route", rt.Name, "backend", backendName, "err", err)
		resp = Response{Status: http.StatusBadGateway, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"error":"bad gateway"}` + "\n")}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Served-By", backendName)
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)

	elapsed := time.Since(start)
	f.reqTotal.Inc(rt.Name, backendName, strconv.Itoa(resp.Status))
	f.reqDuration.Observe(elapsed.Seconds(), rt.Name, backendName)
	f.cfg.Log.Info("request", "route", rt.Name, "method", r.Method, "path", r.URL.Path, "backend", backendName,
		"status", resp.Status, "duration_ms", float64(elapsed.Microseconds())/1000)

	shadow := rt.Shadowable && !useModern && err == nil &&
		(flag.Mode == ModeShadow || (flag.Mode == ModeCanary && flag.Shadow))
	if shadow {
		u := *r.URL
		f.enqueue(shadowJob{route: rt.Name, method: r.Method, url: &u, header: r.Header.Clone(), body: body, primary: resp})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (f *Facade) enqueue(j shadowJob) {
	f.inflight.Add(1)
	select {
	case f.jobs <- j:
	default:
		f.inflight.Done()
		f.shadowDrop.Inc(j.route)
	}
}

func (f *Facade) shadowWorker() {
	defer f.workers.Done()
	for j := range f.jobs {
		f.runShadow(j)
		f.inflight.Done()
	}
}

func (f *Facade) runShadow(j shadowJob) {
	ctx, cancel := context.WithTimeout(context.Background(), f.cfg.ShadowTimeout)
	defer cancel()
	f.shadowTotal.Inc(j.route)
	start := time.Now()
	got, err := f.cfg.Modern.Do(ctx, j.method, j.url, j.header, j.body)
	f.shadowDur.Observe(time.Since(start).Seconds(), j.route)
	if err != nil {
		f.shadowErr.Inc(j.route)
		f.cfg.Log.Warn("sombra falló", "route", j.route, "err", err)
		return
	}
	var diffs []Diff
	if got.Status != j.primary.Status {
		diffs = append(diffs, Diff{Path: "$status", Legacy: strconv.Itoa(j.primary.Status), Modern: strconv.Itoa(got.Status)})
	}
	bodyDiffs, err := CompareJSON(j.primary.Body, got.Body)
	if err != nil {
		f.shadowErr.Inc(j.route)
		f.cfg.Log.Warn("comparación falló", "route", j.route, "err", err)
		return
	}
	diffs = append(diffs, bodyDiffs...)
	var rules []Rule
	if f.cfg.Accepted != nil {
		rules = f.cfg.Accepted(j.route)
	}
	mismatch, accepted := Classify(diffs, rules)
	switch {
	case len(mismatch) > 0:
		f.shadowMiss.Inc(j.route)
		seen := map[string]bool{}
		for _, d := range mismatch {
			p := NormalizePath(d.Path)
			if !seen[p] {
				seen[p] = true
				f.shadowFields.Inc(j.route, p)
			}
		}
		f.record(MismatchRecord{Route: j.route, URL: j.url.RequestURI(), LegacyStatus: j.primary.Status, ModernStatus: got.Status, Diffs: firstN(mismatch, 10)})
		f.cfg.Log.Warn("shadow mismatch", "route", j.route, "url", j.url.RequestURI(), "diffs", len(mismatch))
	case len(accepted) > 0:
		f.shadowAccept.Inc(j.route)
	}
}

func firstN(d []Diff, n int) []Diff {
	if len(d) > n {
		return d[:n]
	}
	return d
}

func (f *Facade) record(m MismatchRecord) {
	f.repMu.Lock()
	defer f.repMu.Unlock()
	f.recent = append(f.recent, m)
	if len(f.recent) > maxRecent {
		f.recent = f.recent[len(f.recent)-maxRecent:]
	}
}

// RouteReport son los contadores de sombra de una ruta.
type RouteReport struct {
	Shadowed   float64 `json:"shadowed"`
	Mismatches float64 `json:"mismatches"`
	Accepted   float64 `json:"accepted_only"`
	Dropped    float64 `json:"dropped"`
	Errors     float64 `json:"errors"`
	Fallbacks  float64 `json:"fallbacks"`
}

// Report es el reporte de diferencias.
type Report struct {
	Routes map[string]RouteReport `json:"routes"`
	Recent []MismatchRecord       `json:"recent_mismatches"`
}

// Report arma el reporte actual.
func (f *Facade) Report() Report {
	rep := Report{Routes: map[string]RouteReport{}}
	names := make([]string, 0, len(f.routes))
	for n := range f.routes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rep.Routes[n] = RouteReport{
			Shadowed: f.shadowTotal.Value(n), Mismatches: f.shadowMiss.Value(n), Accepted: f.shadowAccept.Value(n),
			Dropped: f.shadowDrop.Value(n), Errors: f.shadowErr.Value(n), Fallbacks: f.fallback.Value(n),
		}
	}
	f.repMu.Lock()
	rep.Recent = append([]MismatchRecord{}, f.recent...)
	f.repMu.Unlock()
	return rep
}

// WaitShadow espera a que se procese toda la sombra encolada (tests y bench).
func (f *Facade) WaitShadow() { f.inflight.Wait() }

// Close detiene los workers de sombra después de vaciar la cola.
func (f *Facade) Close() {
	f.closeOne.Do(func() {
		f.inflight.Wait()
		close(f.jobs)
		f.workers.Wait()
	})
}

func (f *Facade) adminGetFlags(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.cfg.Flags.Snapshot())
}

func (f *Facade) adminSetFlag(w http.ResponseWriter, r *http.Request) {
	var rf RouteFlag
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rf); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := f.SetFlag(r.PathValue("route"), rf); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUnknownRoute) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	f.cfg.Log.Info("flag cambiado", "route", r.PathValue("route"), "mode", rf.Mode, "percent", rf.Percent, "shadow", rf.Shadow)
	w.WriteHeader(http.StatusNoContent)
}

var errUnknownRoute = errors.New("ruta desconocida")
