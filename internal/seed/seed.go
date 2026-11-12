// Package seed crea el esquema legado y carga datos sintéticos deterministas.
//
// Los datos son inventados (nombres de una lista fija, teléfonos al azar) y se
// generan con un PRNG con semilla fija: la misma Config produce siempre la
// misma base, byte a byte en contenido. Eso es lo que hace comparables las
// mediciones de antes y después.
package seed

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
)

// LegacySchema es el esquema tal como lo dejó el sistema legado: sin índices
// secundarios y con el teléfono como texto libre.
const LegacySchema = `
CREATE TABLE agents (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL,
  team  TEXT NOT NULL
);
CREATE TABLE customers (
  id          INTEGER PRIMARY KEY,
  first_name  TEXT NOT NULL,
  last_name   TEXT NOT NULL,
  email       TEXT NOT NULL,
  phone       TEXT,
  segment     TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE TABLE tickets (
  id           INTEGER PRIMARY KEY,
  customer_id  INTEGER NOT NULL REFERENCES customers(id),
  agent_id     INTEGER REFERENCES agents(id),
  subject      TEXT NOT NULL,
  status       TEXT NOT NULL,
  priority     INTEGER NOT NULL,
  created_at   TEXT NOT NULL
);
CREATE TABLE interactions (
  id          INTEGER PRIMARY KEY,
  ticket_id   INTEGER NOT NULL REFERENCES tickets(id),
  agent_id    INTEGER REFERENCES agents(id),
  channel     TEXT NOT NULL,
  body        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
`

// TimeLayout es el formato con el que se guardan las fechas (UTC).
const TimeLayout = "2006-01-02T15:04:05Z"

// Config define el tamaño del dataset.
type Config struct {
	Customers    int
	Agents       int
	Tickets      int
	Interactions int
	Seed         uint64
}

// Small es el dataset de los tests y del golden master.
var Small = Config{Customers: 30, Agents: 5, Tickets: 120, Interactions: 500, Seed: 7}

// Bench es el dataset del benchmark: chico a propósito para la box.
var Bench = Config{Customers: 800, Agents: 30, Tickets: 6000, Interactions: 30000, Seed: 42}

var (
	firstNames = []string{"Ana", "Bruno", "Carla", "Diego", "Elena", "Facundo", "Gabriela", "Hernán", "Inés", "Julián", "Lucía", "Martín", "Natalia", "Óscar", "Paula", "Ramiro", "Sofía", "Tomás", "Valeria", "Ñandú"}
	lastNames  = []string{"Acosta", "Benítez", "Castro", "Domínguez", "Fernández", "Gómez", "Herrera", "Ibáñez", "Juárez", "López", "Medina", "Núñez", "Ortiz", "Pereyra", "Quiroga", "Ruiz", "Sosa", "Torres", "Vega", "Zárate"}
	segments   = []string{"retail", "pyme", "corporativo"}
	teams      = []string{"nivel-1", "nivel-2", "cobranzas"}
	subjects   = []string{"No llega el pedido", "Reclamo de facturación", "Cambio de domicilio", "Producto dañado", "Devolución pendiente", "Consulta de envío", "Baja del servicio", "Error al pagar <tarjeta>"}
	channels   = []string{"email", "phone", "chat", "whatsapp"}
	bodies     = []string{"Hola, necesito ayuda", "Ya lo revisamos", "¿Me confirman el número de seguimiento?", "Se reprocesó el pago", "Gracias & saludos", "Escalado a nivel 2", "Cliente no responde"}
)

// Base es el instante cero de los datos sintéticos.
var Base = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

// Load crea el esquema legado y carga los datos de cfg en db.
func Load(ctx context.Context, db *store.DB, cfg Config) error {
	if _, err := db.ExecContext(ctx, LegacySchema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	r := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15))
	tx, err := db.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for i := 1; i <= cfg.Agents; i++ {
		name := fmt.Sprintf("%s %s", firstNames[r.IntN(len(firstNames))], lastNames[r.IntN(len(lastNames))])
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents(id,name,team) VALUES(?,?,?)`, i, name, teams[r.IntN(len(teams))]); err != nil {
			return err
		}
	}
	for i := 1; i <= cfg.Customers; i++ {
		fn := firstNames[r.IntN(len(firstNames))]
		ln := lastNames[r.IntN(len(lastNames))]
		email := fmt.Sprintf("cliente%04d@example.com", i)
		created := Base.Add(time.Duration(r.IntN(60*24*30)) * time.Minute)
		var phone any = randomPhone(r)
		if phone == "" && r.IntN(2) == 0 {
			phone = nil // algunos NULL y algunos vacíos: el legado tiene ambos
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO customers(id,first_name,last_name,email,phone,segment,created_at) VALUES(?,?,?,?,?,?,?)`,
			i, fn, ln, email, phone, segments[r.IntN(len(segments))], created.Format(TimeLayout)); err != nil {
			return err
		}
	}
	// El 10% final de clientes nunca abre tickets (para el caso "tickets": null).
	withTickets := cfg.Customers - cfg.Customers/10
	ticketCreated := make([]time.Time, cfg.Tickets+1)
	ticketAgent := make([]int, cfg.Tickets+1)
	for i := 1; i <= cfg.Tickets; i++ {
		cust := 1 + r.IntN(withTickets)
		var agent any
		if r.IntN(100) < 85 {
			a := 1 + r.IntN(cfg.Agents)
			agent, ticketAgent[i] = a, a
		}
		status := "closed"
		switch p := r.IntN(100); {
		case p < 30:
			status = "open"
		case p < 50:
			status = "pending"
		}
		prio := 1 + r.IntN(3)
		if r.IntN(100) < 3 {
			prio = 4 // valor fuera de catálogo que el legado nunca validó
		}
		created := Base.Add(time.Duration(r.IntN(60*24*300)) * time.Minute)
		ticketCreated[i] = created
		if _, err := tx.ExecContext(ctx, `INSERT INTO tickets(id,customer_id,agent_id,subject,status,priority,created_at) VALUES(?,?,?,?,?,?,?)`,
			i, cust, agent, subjects[r.IntN(len(subjects))], status, prio, created.Format(TimeLayout)); err != nil {
			return err
		}
	}
	lastAt := make([]time.Time, cfg.Tickets+1)
	for i := 1; i <= cfg.Interactions; i++ {
		t := 1 + r.IntN(cfg.Tickets)
		if lastAt[t].IsZero() {
			lastAt[t] = ticketCreated[t]
		}
		at := lastAt[t].Add(time.Duration(1+r.IntN(600)) * time.Minute)
		lastAt[t] = at
		var agent any
		if r.IntN(100) < 60 {
			if ticketAgent[t] != 0 && r.IntN(100) < 80 {
				agent = ticketAgent[t]
			} else {
				agent = 1 + r.IntN(cfg.Agents)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO interactions(id,ticket_id,agent_id,channel,body,created_at) VALUES(?,?,?,?,?,?)`,
			i, t, agent, channels[r.IntN(len(channels))], bodies[r.IntN(len(bodies))], at.Format(TimeLayout)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// randomPhone imita la suciedad real de un campo de texto libre.
func randomPhone(r *rand.Rand) string {
	a, b := r.IntN(10000), r.IntN(10000)
	switch p := r.IntN(100); {
	case p < 25:
		return fmt.Sprintf("+54 11 %04d-%04d", a, b)
	case p < 45:
		return fmt.Sprintf("011 %04d-%04d", a, b)
	case p < 65:
		return fmt.Sprintf("(11) %04d %04d", a, b)
	case p < 80:
		return fmt.Sprintf("11%04d%04d", a, b)
	case p < 90:
		return fmt.Sprintf("+54 9 11 %04d-%04d", a, b)
	case p < 96:
		return fmt.Sprintf("15-%04d-%04d", a, b) // sin código de área: no normalizable
	default:
		return ""
	}
}
