// Comando modern: levanta el servicio nuevo.
//
//	go run ./cmd/modern -db crm.db -addr localhost:8082
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/logging"
)

func main() {
	dbPath := flag.String("db", "crm.db", "archivo SQLite")
	addr := flag.String("addr", "localhost:8082", "dirección de escucha")
	flag.Parse()
	logger := logging.New("modern", nil, false)
	ctx := context.Background()
	env, err := app.Open(ctx, *dbPath, logger)
	if err != nil {
		log.Fatal(err)
	}
	// La fase de migración la cambia otro proceso (cmd/migrate): se relee.
	go func() {
		for range time.Tick(time.Second) {
			if err := env.Ctl.Refresh(ctx); err != nil {
				logger.Error("refresh fase", "err", err)
			}
		}
	}()
	mux := http.NewServeMux()
	mux.Handle("/", env.Modern.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	logger.Info("escuchando", "addr", *addr, "phase", env.Ctl.Phase().String())
	log.Fatal(http.ListenAndServe(*addr, mux))
}
