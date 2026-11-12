// Package app arma las piezas (base, legado, nuevo, migración, fachada) en
// un solo proceso. Lo usan los tests, el benchmark y los comandos.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/facade"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/modern"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

// Env es un CRM completo sobre una base SQLite.
type Env struct {
	DB     *store.DB
	Ctl    *migration.Controller
	Modern *modern.Service
}

// Seed crea una base nueva en path con el dataset cfg.
func Seed(ctx context.Context, path string, cfg seed.Config) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s ya existe", filepath.Base(path))
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	if err := seed.Load(ctx, db, cfg); err != nil {
		_ = db.Close()
		return err
	}
	return db.Close()
}

// Open abre una base existente y arma el entorno. OJO: el legado usa una
// variable global, así que en un proceso solo puede haber un Env activo a la
// vez (es parte de la deuda que se documenta).
func Open(ctx context.Context, path string, log *slog.Logger) (*Env, error) {
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	ctl, err := migration.NewController(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	legacy.DB = db
	return &Env{DB: db, Ctl: ctl, Modern: modern.NewService(modern.NewRepo(db, ctl), log)}, nil
}

// Close cierra la base.
func (e *Env) Close() error {
	if legacy.DB == e.DB {
		legacy.DB = nil
	}
	return e.DB.Close()
}

// Facade arma una fachada en proceso sobre este entorno.
func (e *Env) Facade(flags *facade.Flags, log *slog.Logger) *facade.Facade {
	f := facade.New(facade.Config{
		Legacy:   facade.HandlerBackend{Handler: legacy.Handler()},
		Modern:   facade.HandlerBackend{Handler: e.Modern.Handler()},
		Flags:    flags,
		Log:      log,
		Accepted: AcceptedDiffs(e.Ctl),
	})
	f.Registry().NewGaugeFunc("db_queries_total", "Consultas SQL ejecutadas por el proceso (contador monótono).", func() float64 { return float64(e.DB.Queries()) })
	f.Registry().NewGaugeFunc("migration_phase", "Fase de la migración expand/contract (0=none .. 6=contracted).", func() float64 { return float64(e.Ctl.Phase()) })
	return f
}

// AcceptedDiffs son las diferencias que se decidieron aceptar (ADR-0004):
// tras reads_switched, el teléfono sale normalizado y ya no es igual al texto
// libre que devuelve el legado. Antes de esa fase no se acepta ninguna.
func AcceptedDiffs(ctl *migration.Controller) func(route string) []facade.Rule {
	reason := "ADR-0004: teléfono normalizado E.164 tras reads_switched"
	rules := map[string][]facade.Rule{
		"customer_get": {{Path: "phone", Reason: reason}},
		"customer_360": {{Path: "customer.phone", Reason: reason}},
	}
	return func(route string) []facade.Rule {
		if ctl.Phase() < migration.ReadsSwitched {
			return nil
		}
		return rules[route]
	}
}

// CopyFile copia una base cerrada (para que cada escenario arranque igual).
func CopyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
