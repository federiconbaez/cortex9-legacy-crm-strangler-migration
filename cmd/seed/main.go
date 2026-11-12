// Comando seed: crea una base SQLite con datos sintéticos.
//
//	go run ./cmd/seed -db crm.db -size bench
package main

import (
	"context"
	"flag"
	"log"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
)

func main() {
	db := flag.String("db", "crm.db", "archivo SQLite a crear (no debe existir)")
	size := flag.String("size", "small", "small | bench")
	flag.Parse()
	cfg := seed.Small
	if *size == "bench" {
		cfg = seed.Bench
	}
	if err := app.Seed(context.Background(), *db, cfg); err != nil {
		log.Fatal(err)
	}
	log.Printf("base creada: %d clientes, %d agentes, %d tickets, %d interacciones", cfg.Customers, cfg.Agents, cfg.Tickets, cfg.Interactions)
}
