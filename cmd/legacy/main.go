// Comando legacy: levanta el monolito heredado tal cual.
//
//	go run ./cmd/legacy -db crm.db -addr localhost:8081
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

func main() {
	dbPath := flag.String("db", "crm.db", "archivo SQLite")
	addr := flag.String("addr", "localhost:8081", "dirección de escucha")
	flag.Parse()
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	legacy.DB = db
	legacy.Verbose = true
	log.Printf("legacy escuchando en %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, legacy.Handler()))
}
