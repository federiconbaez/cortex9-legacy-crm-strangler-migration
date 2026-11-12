// Comando migrate: ejecuta una fase de la migración expand/contract.
//
//	go run ./cmd/migrate -db crm.db -step status
//	go run ./cmd/migrate -db crm.db -step expand
//	go run ./cmd/migrate -db crm.db -step dual-write
//	go run ./cmd/migrate -db crm.db -step backfill -batch 200
//	go run ./cmd/migrate -db crm.db -step verify
//	go run ./cmd/migrate -db crm.db -step switch-reads   (o rollback-reads)
//	go run ./cmd/migrate -db crm.db -step contract -legacy-readers 0
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
)

func main() {
	dbPath := flag.String("db", "crm.db", "archivo SQLite")
	step := flag.String("step", "status", "status|expand|dual-write|backfill|verify|switch-reads|rollback-reads|contract")
	batch := flag.Int("batch", 200, "tamaño de lote del backfill")
	readers := flag.Int("legacy-readers", -1, "rutas que todavía llegan al legado (obligatorio para contract; ver /admin/flags)")
	flag.Parse()
	ctx := context.Background()
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	c, err := migration.NewController(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	var out any
	switch *step {
	case "status":
	case "expand":
		err = c.Expand(ctx)
	case "dual-write":
		err = c.EnableDualWrite(ctx)
	case "backfill":
		out, err = c.Backfill(ctx, *batch)
	case "verify":
		out, err = c.Verify(ctx)
	case "switch-reads":
		err = c.SwitchReads(ctx)
	case "rollback-reads":
		err = c.RollbackReads(ctx)
	case "contract":
		if *readers < 0 {
			log.Fatal("contract exige -legacy-readers explícito (normalmente 0)")
		}
		err = c.Contract(ctx, *readers)
	default:
		log.Fatalf("paso desconocido %q", *step)
	}
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"step": *step, "phase": c.Phase().String(), "report": out})
}
