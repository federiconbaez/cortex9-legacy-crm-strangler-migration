// Comando bench: corre todos los escenarios con los mismos datos y las
// mismas requests y escribe los resultados en results/.
//
//	go run ./cmd/bench -out results            # corrida completa (~1-2 min)
//	go run ./cmd/bench -quick                  # humo para CI: no escribe results/
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/bench"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
)

func main() {
	out := flag.String("out", "results", "directorio de resultados")
	quick := flag.Bool("quick", false, "dataset chico y pocas requests; no escribe resultados")
	requests := flag.Int("requests", 300, "requests medidas por endpoint y escenario")
	conc := flag.Int("concurrency", 4, "clientes concurrentes")
	flag.Parse()

	p := bench.Params{Dataset: seed.Bench, Requests: *requests, Warmup: 20, Calibrate: 20, Concurrency: *conc, Seed: 2026}
	if *quick {
		p = bench.Params{Dataset: seed.Small, Requests: 30, Warmup: 3, Calibrate: 5, Concurrency: 2, Seed: 2026}
	}
	ctx := context.Background()
	work, err := os.MkdirTemp("", "crm-bench-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(work)
	template := filepath.Join(work, "template.db")
	t0 := time.Now()
	if err := app.Seed(ctx, template, p.Dataset); err != nil {
		log.Fatal(err)
	}
	log.Printf("dataset listo en %s", time.Since(t0).Round(time.Millisecond))

	var results []bench.ScenarioResult
	for _, sc := range bench.Scenarios {
		t := time.Now()
		r, err := bench.Run(ctx, sc, template, work, p)
		if err != nil {
			log.Fatalf("%s: %v", sc.Name, err)
		}
		for _, e := range r.Endpoints {
			log.Printf("%-20s %-13s p50=%7.2fms p95=%7.2fms p99=%7.2fms rps=%8.1f q/req=%6.2f err=%d",
				sc.Name, e.Endpoint, e.Latency.P50, e.Latency.P95, e.Latency.P99, e.ThroughputRPS, e.QueriesPerReq, e.Errors)
			if e.Errors > 0 {
				log.Fatalf("%s/%s tuvo %d errores", sc.Name, e.Endpoint, e.Errors)
			}
		}
		log.Printf("%s terminado en %s", sc.Name, time.Since(t).Round(time.Millisecond))
		results = append(results, r)
	}
	if *quick {
		log.Print("modo quick: no se escriben resultados")
		return
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	env := environment(p)
	write(filepath.Join(*out, "bench.json"), map[string]any{"environment": env, "params": p, "scenarios": results})
	if err := os.WriteFile(filepath.Join(*out, "summary.md"), []byte(summary(env, p, results)), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("resultados en %s/", *out)
}

func write(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "desconocido"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return "desconocido"
}

func environment(p bench.Params) map[string]any {
	return map[string]any{
		"date_utc":   time.Now().UTC().Format(time.RFC3339),
		"go":         runtime.Version(),
		"os_arch":    runtime.GOOS + "/" + runtime.GOARCH,
		"num_cpu":    runtime.NumCPU(),
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"cpu_model":  cpuModel(),
		"sqlite":     "modernc.org/sqlite (Go puro), WAL, archivo local, 8 conexiones",
	}
}

func find(rs []bench.ScenarioResult, name, ep string) (bench.EndpointResult, bool) {
	for _, r := range rs {
		if r.Name != name {
			continue
		}
		for _, e := range r.Endpoints {
			if e.Endpoint == ep {
				return e, true
			}
		}
	}
	return bench.EndpointResult{}, false
}

func ratio(a, b float64) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1fx", a/b)
}

func summary(env map[string]any, p bench.Params, rs []bench.ScenarioResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Resultados del benchmark\n\n")
	fmt.Fprintf(&b, "Generado por `go run ./cmd/bench` el %s. No editar a mano: se regenera.\n\n", env["date_utc"])
	fmt.Fprintf(&b, "- Máquina: %v, %v CPU, %v, %v\n", env["cpu_model"], env["num_cpu"], env["os_arch"], env["go"])
	fmt.Fprintf(&b, "- Base: %v\n", env["sqlite"])
	fmt.Fprintf(&b, "- Dataset: %d clientes, %d agentes, %d tickets, %d interacciones (semilla %d)\n",
		p.Dataset.Customers, p.Dataset.Agents, p.Dataset.Tickets, p.Dataset.Interactions, p.Dataset.Seed)
	fmt.Fprintf(&b, "- Por endpoint y escenario: %d de calentamiento, %d en serie para contar consultas, %d medidas con %d clientes concurrentes\n",
		p.Warmup, p.Calibrate, p.Requests, p.Concurrency)
	fmt.Fprintf(&b, "- Latencia medida en el cliente HTTP (loopback, keep-alive), percentiles nearest-rank. Una sola corrida: ver caveats en README.\n\n")

	fmt.Fprintf(&b, "## Línea base vs. estado final\n\n")
	fmt.Fprintf(&b, "| Endpoint | p50 base → final (ms) | p95 base → final (ms) | p99 base → final (ms) | rps base → final | consultas/req base → final | mejora p95 |\n|---|---|---|---|---|---|---|\n")
	for _, ep := range bench.Endpoints {
		a, ok1 := find(rs, "baseline-legacy", ep.Name)
		z, ok2 := find(rs, "final-modern", ep.Name)
		if !ok1 || !ok2 {
			continue
		}
		fmt.Fprintf(&b, "| %s | %.2f → %.2f | %.2f → %.2f | %.2f → %.2f | %.1f → %.1f | %.2f → %.2f | %s |\n", ep.Name,
			a.Latency.P50, z.Latency.P50, a.Latency.P95, z.Latency.P95, a.Latency.P99, z.Latency.P99,
			a.ThroughputRPS, z.ThroughputRPS, a.QueriesPerReq, z.QueriesPerReq, ratio(a.Latency.P95, z.Latency.P95))
	}
	fmt.Fprintf(&b, "\n## Todos los escenarios\n\n")
	for _, ep := range bench.Endpoints {
		fmt.Fprintf(&b, "### %s\n\n| Escenario | p50 ms | p95 ms | p99 ms | max ms | rps | consultas/req (min–max) | errores |\n|---|---|---|---|---|---|---|---|\n", ep.Name)
		for _, r := range rs {
			e, ok := find(rs, r.Name, ep.Name)
			if !ok {
				continue
			}
			note := ""
			if e.ShadowIncluded {
				note = " (incluye sombra)"
			}
			fmt.Fprintf(&b, "| %s | %.2f | %.2f | %.2f | %.2f | %.1f | %.2f (%d–%d)%s | %d |\n", r.Name,
				e.Latency.P50, e.Latency.P95, e.Latency.P99, e.Latency.Max, e.ThroughputRPS, e.QueriesPerReq, e.QueriesMin, e.QueriesMax, note, e.Errors)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Escenarios\n\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "- **%s** (fase `%s`): %s\n", r.Name, r.Phase, r.Description)
	}
	for _, r := range rs {
		if r.ShadowReport == nil {
			continue
		}
		fmt.Fprintf(&b, "\n## Comparador en sombra (%s)\n\n| Ruta | en sombra | mismatches | solo aceptadas | descartadas | errores |\n|---|---|---|---|---|---|\n", r.Name)
		for _, ep := range bench.Endpoints {
			s := r.ShadowReport[ep.Name]
			fmt.Fprintf(&b, "| %s | %.0f | %.0f | %.0f | %.0f | %.0f |\n", ep.Name, s.Shadowed, s.Mismatches, s.Accepted, s.Dropped, s.Errors)
		}
	}
	return b.String()
}
