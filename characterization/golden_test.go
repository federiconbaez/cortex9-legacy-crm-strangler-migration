package characterization

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/facade"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

// TestGoldenLegacy fija (o con -update regenera) el comportamiento del legado.
func TestGoldenLegacy(t *testing.T) {
	newEnv(t)
	h := legacy.Handler()
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath("x")), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, s := range script {
			code, body := do(t, h, s)
			g := golden{Request: s, Status: code, Body: json.RawMessage(canonical(t, body))}
			out, _ := json.MarshalIndent(g, "", "  ")
			if err := os.WriteFile(goldenPath(s.Name), append(out, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("regenerados %d golden", len(script))
		return
	}
	runScript(t, h, allSteps)
}

// TestGoldenModern: el servicio nuevo, solo, reproduce el contrato heredado.
func TestGoldenModern(t *testing.T) {
	env := newEnv(t)
	runScript(t, env.Modern.Handler(), allSteps)
}

// TestGoldenThroughFacade: el contrato se mantiene en cada modo de la fachada.
func TestGoldenThroughFacade(t *testing.T) {
	modes := map[string]facade.RouteFlag{
		"legacy":    {Mode: facade.ModeLegacy},
		"shadow":    {Mode: facade.ModeShadow},
		"canary-50": {Mode: facade.ModeCanary, Percent: 50, Shadow: true},
		"modern":    {Mode: facade.ModeModern},
	}
	for name, rf := range modes {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			f := env.Facade(facade.NewFlags(), nil)
			defer f.Close()
			for _, r := range facade.DefaultRoutes() {
				flag := rf
				if !r.Shadowable {
					flag.Shadow = false
					if flag.Mode == facade.ModeShadow {
						flag = facade.RouteFlag{Mode: facade.ModeLegacy}
					}
				}
				if err := f.SetFlag(r.Name, flag); err != nil {
					t.Fatal(err)
				}
			}
			runScript(t, f, allSteps)
			f.WaitShadow()
			for route, rr := range f.Report().Routes {
				if rr.Mismatches != 0 || rr.Errors != 0 {
					t.Errorf("%s: la sombra encontró diferencias: %+v %+v", route, rr, f.Report().Recent)
				}
			}
		})
	}
}

// TestGoldenDuringMigration: mientras las lecturas siguen en la columna vieja
// (expand, dual-write, backfill, verify), el contrato no cambia nada.
func TestGoldenDuringMigration(t *testing.T) {
	env := newEnv(t)
	migrateTo(t, env, migration.Verified)
	runScript(t, env.Modern.Handler(), allSteps)

	// El legado también sigue igual con el esquema expandido: índices y
	// columna nueva no le cambian nada (base nueva, porque el guion escribe).
	env2 := newEnv(t)
	migrateTo(t, env2, migration.Verified)
	runScript(t, legacy.Handler(), allSteps)
}

// TestGoldenAfterReadSwitch: tras reads_switched la única diferencia permitida
// es el teléfono (ADR-0004). Todo lo demás sigue igual al golden.
func TestGoldenAfterReadSwitch(t *testing.T) {
	env := newEnv(t)
	migrateTo(t, env, migration.ReadsSwitched)
	runScript(t, env.Modern.Handler(), allSteps, "phone", "customer.phone")
}

// TestGoldenAfterContract: con la columna vieja borrada el servicio nuevo
// sigue cumpliendo el contrato (salvo el teléfono aceptado).
func TestGoldenAfterContract(t *testing.T) {
	env := newEnv(t)
	migrateTo(t, env, migration.ReadsSwitched)
	if err := env.Ctl.Contract(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	runScript(t, env.Modern.Handler(), allSteps, "phone", "customer.phone")
}

// migrateTo avanza la migración hasta la fase pedida (sin contract).
func migrateTo(t *testing.T, env *app.Env, target migration.Phase) {
	t.Helper()
	ctx := context.Background()
	steps := []func() error{
		func() error { return env.Ctl.Expand(ctx) },
		func() error { return env.Ctl.EnableDualWrite(ctx) },
		func() error { _, err := env.Ctl.Backfill(ctx, 7); return err },
		func() error { _, err := env.Ctl.Verify(ctx); return err },
		func() error { return env.Ctl.SwitchReads(ctx) },
	}
	for i, s := range steps {
		if env.Ctl.Phase() >= target {
			break
		}
		if err := s(); err != nil {
			t.Fatalf("paso %d: %v", i, err)
		}
	}
	if env.Ctl.Phase() != target {
		t.Fatalf("fase %s, quería %s", env.Ctl.Phase(), target)
	}
}
