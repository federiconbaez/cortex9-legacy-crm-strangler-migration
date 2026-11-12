// Comando facade: la fachada strangler delante del legado y del nuevo.
//
//	go run ./cmd/facade -legacy http://localhost:8081 -modern http://localhost:8082 \
//	    -db crm.db -flags flags.json -admin
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/facade"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/logging"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
)

func main() {
	legacyURL := flag.String("legacy", "http://localhost:8081", "URL del legado")
	modernURL := flag.String("modern", "http://localhost:8082", "URL del servicio nuevo")
	addr := flag.String("addr", "localhost:8080", "dirección de escucha")
	dbPath := flag.String("db", "crm.db", "base (solo para leer la fase de migración)")
	flagsPath := flag.String("flags", "", "JSON con flags iniciales por ruta")
	admin := flag.Bool("admin", false, "habilita /admin (sin autenticación: solo local)")
	flag.Parse()

	logger := logging.New("facade", nil, false)
	lu, err := url.Parse(*legacyURL)
	if err != nil {
		log.Fatal(err)
	}
	mu, err := url.Parse(*modernURL)
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	ctl, err := migration.NewController(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for range time.Tick(time.Second) {
			_ = ctl.Refresh(ctx)
		}
	}()
	flags := facade.NewFlags()
	if *flagsPath != "" {
		f, err := os.Open(*flagsPath)
		if err != nil {
			log.Fatal(err)
		}
		if err := flags.Load(f); err != nil {
			log.Fatal(err)
		}
		_ = f.Close()
	}
	client := &http.Client{Timeout: 5 * time.Second}
	fc := facade.New(facade.Config{
		Legacy:   facade.HTTPBackend{Base: lu, Client: client},
		Modern:   facade.HTTPBackend{Base: mu, Client: client},
		Flags:    flags,
		Log:      logger,
		Accepted: app.AcceptedDiffs(ctl),
		Admin:    *admin,
	})
	fc.Registry().NewGaugeFunc("migration_phase", "Fase de la migración expand/contract (0=none .. 6=contracted).", func() float64 { return float64(ctl.Phase()) })
	logger.Info("escuchando", "addr", *addr, "legacy", lu.Host, "modern", mu.Host)
	log.Fatal(http.ListenAndServe(*addr, fc))
}
