package migration_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPhasesMustRunInOrder(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	c := env.Ctl
	if _, err := c.Backfill(ctx, 10); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatalf("backfill sin dual-write debería fallar: %v", err)
	}
	if err := c.EnableDualWrite(ctx); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatal("dual-write sin expand debería fallar")
	}
	if err := c.SwitchReads(ctx); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatal("switch sin verify debería fallar")
	}
	if err := c.Contract(ctx, 0); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatal("contract sin switch debería fallar")
	}
	must(t, c.Expand(ctx))
	must(t, c.Expand(ctx)) // idempotente
	must(t, c.EnableDualWrite(ctx))
	if c.Phase() != migration.DualWrite {
		t.Fatal(c.Phase())
	}
}

func TestBackfillIsIdempotentAndQuarantinesInvalid(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	c := env.Ctl
	must(t, c.Expand(ctx))
	must(t, c.EnableDualWrite(ctx))
	r1, err := c.Backfill(ctx, 7)
	must(t, err)
	if r1.Scanned != seed.Small.Customers || r1.Batches != (seed.Small.Customers+6)/7 {
		t.Fatalf("reporte %+v", r1)
	}
	if r1.Quarantined == 0 || r1.Updated == 0 {
		t.Fatalf("esperaba válidos e inválidos en los datos: %+v", r1)
	}
	r2, err := c.Backfill(ctx, 7)
	must(t, err)
	if r2.Updated != 0 {
		t.Fatalf("segunda corrida no debería escribir nada: %+v", r2)
	}
	v, err := c.Verify(ctx)
	must(t, err)
	if v.Drift != 0 || v.Quarantined != r1.Quarantined || c.Phase() != migration.Verified {
		t.Fatalf("verify %+v fase %s", v, c.Phase())
	}
	// Cada inválido conserva su texto original en la cuarentena.
	var n int
	must(t, env.DB.QueryRow(`SELECT COUNT(*) FROM customers c JOIN customer_phone_quarantine q ON q.customer_id = c.id WHERE COALESCE(c.phone,'') = q.raw AND c.phone_e164 IS NULL`).Scan(&n))
	if n != r1.Quarantined {
		t.Fatalf("cuarentena %d != %d", n, r1.Quarantined)
	}
}

// Un escritor que no hace dual-write (el legado) genera drift; verify lo
// detecta, bloquea el switch, y un nuevo backfill lo repara.
func TestVerifyDetectsDriftFromLegacyWriter(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	c := env.Ctl
	must(t, c.Expand(ctx))
	must(t, c.EnableDualWrite(ctx))
	_, err := c.Backfill(ctx, 100)
	must(t, err)
	_, err = c.Verify(ctx)
	must(t, err)

	rec := httptest.NewRecorder()
	legacy.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/customers/2/phone", strings.NewReader(`{"phone":"011 4999-0000"}`)))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	v, err := c.Verify(ctx)
	must(t, err)
	if v.Drift != 1 || len(v.DriftIDs) != 1 || v.DriftIDs[0] != 2 {
		t.Fatalf("esperaba drift en el cliente 2: %+v", v)
	}
	if err := c.SwitchReads(ctx); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatalf("switch con drift debería fallar: %v", err)
	}
	r, err := c.Backfill(ctx, 100)
	must(t, err)
	if r.Updated != 1 {
		t.Fatalf("el backfill de reparación debería tocar 1 fila: %+v", r)
	}
	v, err = c.Verify(ctx)
	must(t, err)
	if v.Drift != 0 {
		t.Fatal(v)
	}
	must(t, c.SwitchReads(ctx))
}

func TestDualWriteKeepsColumnsInSync(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	c := env.Ctl
	must(t, c.Expand(ctx))
	must(t, c.EnableDualWrite(ctx))
	h := env.Modern.Handler()
	for id, p := range map[string]string{"1": "(11) 4321 0000", "2": "15-1111-2222", "3": ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("PUT", "/customers/"+id+"/phone", strings.NewReader(`{"phone":"`+p+`"}`)))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	var e164 *string
	must(t, env.DB.QueryRow(`SELECT phone_e164 FROM customers WHERE id = 1`).Scan(&e164))
	if e164 == nil || *e164 != "+541143210000" {
		t.Fatalf("dual-write no escribió la columna nueva: %v", e164)
	}
	var raw string
	must(t, env.DB.QueryRow(`SELECT raw FROM customer_phone_quarantine WHERE customer_id = 2`).Scan(&raw))
	if raw != "15-1111-2222" {
		t.Fatal(raw)
	}
}

func TestContractRequiresNoLegacyReadersAndKeepsData(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	c := env.Ctl
	must(t, c.Expand(ctx))
	must(t, c.EnableDualWrite(ctx))
	_, err := c.Backfill(ctx, 10)
	must(t, err)
	_, err = c.Verify(ctx)
	must(t, err)
	must(t, c.SwitchReads(ctx))
	must(t, c.RollbackReads(ctx)) // se puede volver atrás antes del contract
	if c.Phase() != migration.Verified {
		t.Fatal(c.Phase())
	}
	must(t, c.SwitchReads(ctx))

	var before int
	must(t, env.DB.QueryRow(`SELECT COUNT(*) FROM customers WHERE phone_e164 IS NOT NULL`).Scan(&before))
	if err := c.Contract(ctx, 2); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatalf("contract con lectores en el legado debería fallar: %v", err)
	}
	must(t, c.Contract(ctx, 0))
	var cols int
	must(t, env.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('customers') WHERE name = 'phone'`).Scan(&cols))
	if cols != 0 || c.Phase() != migration.Contracted {
		t.Fatal("la columna vieja debería haberse borrado")
	}
	var after, quarantined int
	must(t, env.DB.QueryRow(`SELECT COUNT(*) FROM customers WHERE phone_e164 IS NOT NULL`).Scan(&after))
	must(t, env.DB.QueryRow(`SELECT COUNT(*) FROM customer_phone_quarantine`).Scan(&quarantined))
	if after != before || after+quarantined != seed.Small.Customers {
		t.Fatalf("se perdieron datos: e164=%d->%d cuarentena=%d", before, after, quarantined)
	}
	if err := c.RollbackReads(ctx); !errors.Is(err, migration.ErrPrecondition) {
		t.Fatal("después del contract no hay vuelta atrás")
	}
	// El controlador persiste la fase: otro proceso la lee igual.
	c2, err := migration.NewController(ctx, env.DB)
	must(t, err)
	if c2.Phase() != migration.Contracted {
		t.Fatal(c2.Phase())
	}
}
