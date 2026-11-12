// Package migration implementa la migración expand/contract del teléfono
// del cliente: de customers.phone (texto libre) a customers.phone_e164
// (normalizado), sin downtime.
//
// Fases, en orden (cada una es reversible salvo la última):
//
//	none -> expanded -> dual_write -> backfilled -> verified -> reads_switched -> contracted
//
// El estado se persiste en la tabla migration_state y se mantiene en memoria
// en un Controller para que la ruta de lectura no pague una consulta extra.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/phone"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
)

// Phase es la fase actual de la migración.
type Phase int

// Fases de la migración expand/contract.
const (
	None Phase = iota
	Expanded
	DualWrite
	Backfilled
	Verified
	ReadsSwitched
	Contracted
)

var phaseNames = []string{"none", "expanded", "dual_write", "backfilled", "verified", "reads_switched", "contracted"}

func (p Phase) String() string {
	if p < 0 || int(p) >= len(phaseNames) {
		return fmt.Sprintf("phase(%d)", int(p))
	}
	return phaseNames[p]
}

func parsePhase(s string) (Phase, error) {
	for i, n := range phaseNames {
		if n == s {
			return Phase(i), nil
		}
	}
	return None, fmt.Errorf("fase desconocida %q", s)
}

const migrationName = "customers_phone_e164"

// ErrPrecondition se devuelve cuando una fase no puede ejecutarse todavía.
var ErrPrecondition = errors.New("migration: precondición no cumplida")

// Controller guarda la fase en memoria y en la base.
type Controller struct {
	db    *store.DB
	phase atomic.Int32
}

// NewController crea la tabla de estado si falta y carga la fase actual.
func NewController(ctx context.Context, db *store.DB) (*Controller, error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migration_state (name TEXT PRIMARY KEY, phase TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	c := &Controller{db: db}
	return c, c.Refresh(ctx)
}

// Refresh relee la fase desde la base (para procesos separados).
func (c *Controller) Refresh(ctx context.Context) error {
	var s string
	err := c.db.QueryRowContext(ctx, `SELECT phase FROM migration_state WHERE name = ?`, migrationName).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		c.phase.Store(int32(None))
		return nil
	}
	if err != nil {
		return err
	}
	p, err := parsePhase(s)
	if err != nil {
		return err
	}
	c.phase.Store(int32(p))
	return nil
}

// Phase devuelve la fase en memoria (sin consulta).
func (c *Controller) Phase() Phase { return Phase(c.phase.Load()) }

func (c *Controller) set(ctx context.Context, p Phase) error {
	if _, err := c.db.ExecContext(ctx, `INSERT INTO migration_state(name, phase) VALUES(?, ?) ON CONFLICT(name) DO UPDATE SET phase = excluded.phase`, migrationName, p.String()); err != nil {
		return err
	}
	c.phase.Store(int32(p))
	return nil
}

func (c *Controller) require(min Phase) error {
	if c.Phase() < min {
		return fmt.Errorf("%w: hace falta fase >= %s y la actual es %s", ErrPrecondition, min, c.Phase())
	}
	return nil
}

// IndexStatements son los índices que el legado nunca tuvo. Crearlos es
// aditivo: el código viejo sigue funcionando (y también se beneficia).
var IndexStatements = []string{
	`CREATE INDEX IF NOT EXISTS idx_tickets_customer ON tickets(customer_id)`,
	`CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status)`,
	`CREATE INDEX IF NOT EXISTS idx_interactions_ticket ON interactions(ticket_id)`,
}

// ExpandIndexes crea solo los índices (lo usa el benchmark para aislar su efecto).
func ExpandIndexes(ctx context.Context, db *store.DB) error {
	for _, s := range IndexStatements {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// Expand agrega lo nuevo sin quitar nada: índices, columna nullable y tabla
// de cuarentena. Es idempotente.
func (c *Controller) Expand(ctx context.Context) error {
	if c.Phase() >= Expanded {
		return nil
	}
	if err := ExpandIndexes(ctx, c.db); err != nil {
		return err
	}
	has, err := hasColumn(ctx, c.db, "customers", "phone_e164")
	if err != nil {
		return err
	}
	if !has {
		if _, err := c.db.ExecContext(ctx, `ALTER TABLE customers ADD COLUMN phone_e164 TEXT`); err != nil {
			return err
		}
	}
	if _, err := c.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS customer_phone_quarantine (customer_id INTEGER PRIMARY KEY REFERENCES customers(id), raw TEXT NOT NULL)`); err != nil {
		return err
	}
	return c.set(ctx, Expanded)
}

// EnableDualWrite hace que las escrituras nuevas actualicen ambas columnas.
// Tiene que estar activo ANTES del backfill: si no, lo que se escriba durante
// el backfill queda desincronizado.
func (c *Controller) EnableDualWrite(ctx context.Context) error {
	if err := c.require(Expanded); err != nil {
		return err
	}
	if c.Phase() >= DualWrite {
		return nil
	}
	return c.set(ctx, DualWrite)
}

// BackfillReport resume una corrida de backfill.
type BackfillReport struct {
	Scanned, Updated, Quarantined, Batches int
}

// Backfill recorre customers por lotes de batchSize ordenados por id y
// completa phone_e164. Es idempotente y reanudable: solo escribe filas cuyo
// valor difiere del esperado.
func (c *Controller) Backfill(ctx context.Context, batchSize int) (BackfillReport, error) {
	var rep BackfillReport
	if err := c.require(DualWrite); err != nil {
		return rep, err
	}
	if c.Phase() >= Contracted {
		return rep, fmt.Errorf("%w: la columna vieja ya no existe", ErrPrecondition)
	}
	if batchSize <= 0 {
		batchSize = 200
	}
	last := 0
	for {
		type row struct {
			id   int
			raw  sql.NullString
			e164 sql.NullString
		}
		rows, err := c.db.QueryContext(ctx, `SELECT id, phone, phone_e164 FROM customers WHERE id > ? ORDER BY id LIMIT ?`, last, batchSize)
		if err != nil {
			return rep, err
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.raw, &r.e164); err != nil {
				rows.Close()
				return rep, err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rep, err
		}
		if len(batch) == 0 {
			break
		}
		tx, err := c.db.BeginTx(ctx)
		if err != nil {
			return rep, err
		}
		for _, r := range batch {
			rep.Scanned++
			n, err := phone.Normalize(r.raw.String)
			if err != nil {
				rep.Quarantined++
				if _, err := tx.ExecContext(ctx, `INSERT INTO customer_phone_quarantine(customer_id, raw) VALUES(?, ?) ON CONFLICT(customer_id) DO UPDATE SET raw = excluded.raw`, r.id, r.raw.String); err != nil {
					_ = tx.Rollback()
					return rep, err
				}
				if r.e164.Valid {
					if _, err := tx.ExecContext(ctx, `UPDATE customers SET phone_e164 = NULL WHERE id = ?`, r.id); err != nil {
						_ = tx.Rollback()
						return rep, err
					}
					rep.Updated++
				}
				continue
			}
			if !r.e164.Valid || r.e164.String != n {
				if _, err := tx.ExecContext(ctx, `UPDATE customers SET phone_e164 = ? WHERE id = ?`, n, r.id); err != nil {
					_ = tx.Rollback()
					return rep, err
				}
				rep.Updated++
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM customer_phone_quarantine WHERE customer_id = ?`, r.id); err != nil {
				_ = tx.Rollback()
				return rep, err
			}
		}
		if err := tx.Commit(); err != nil {
			return rep, err
		}
		rep.Batches++
		last = batch[len(batch)-1].id
	}
	if c.Phase() == DualWrite {
		return rep, c.set(ctx, Backfilled)
	}
	return rep, nil
}

// VerifyReport es el resultado de comparar columna vieja contra nueva.
type VerifyReport struct {
	Rows        int `json:"rows"`
	Drift       int `json:"drift"`
	Quarantined int `json:"quarantined"`
	// DriftIDs lista hasta 20 ids desincronizados, para depurar.
	DriftIDs []int `json:"drift_ids,omitempty"`
}

// Verify compara fila por fila. Drift = filas donde phone_e164 no es lo que
// produciría normalizar phone, o un inválido que no está en cuarentena.
func (c *Controller) Verify(ctx context.Context) (VerifyReport, error) {
	var rep VerifyReport
	if err := c.require(Backfilled); err != nil {
		return rep, err
	}
	if c.Phase() >= Contracted {
		return rep, nil // ya no hay columna vieja contra la cual comparar
	}
	rep, err := c.verify(ctx)
	if err != nil {
		return rep, err
	}
	if rep.Drift == 0 && c.Phase() == Backfilled {
		return rep, c.set(ctx, Verified)
	}
	return rep, nil
}

// verify hace la comparación sin mirar ni cambiar la fase.
func (c *Controller) verify(ctx context.Context) (VerifyReport, error) {
	var rep VerifyReport
	rows, err := c.db.QueryContext(ctx, `SELECT c.id, c.phone, c.phone_e164, q.raw FROM customers c LEFT JOIN customer_phone_quarantine q ON q.customer_id = c.id ORDER BY c.id`)
	if err != nil {
		return rep, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var raw, e164, q sql.NullString
		if err := rows.Scan(&id, &raw, &e164, &q); err != nil {
			return rep, err
		}
		rep.Rows++
		n, nerr := phone.Normalize(raw.String)
		ok := false
		if nerr != nil {
			ok = !e164.Valid && q.Valid && q.String == raw.String
			if q.Valid {
				rep.Quarantined++
			}
		} else {
			ok = e164.Valid && e164.String == n && !q.Valid
		}
		if !ok {
			rep.Drift++
			if len(rep.DriftIDs) < 20 {
				rep.DriftIDs = append(rep.DriftIDs, id)
			}
		}
	}
	return rep, rows.Err()
}

// SwitchReads hace que el servicio nuevo lea phone_e164. Exige drift 0.
func (c *Controller) SwitchReads(ctx context.Context) error {
	if err := c.require(Verified); err != nil {
		return err
	}
	if c.Phase() >= ReadsSwitched {
		return nil
	}
	rep, err := c.verify(ctx)
	if err != nil {
		return err
	}
	if rep.Drift != 0 {
		return fmt.Errorf("%w: drift=%d", ErrPrecondition, rep.Drift)
	}
	return c.set(ctx, ReadsSwitched)
}

// RollbackReads vuelve a leer la columna vieja (posible hasta contract).
func (c *Controller) RollbackReads(ctx context.Context) error {
	if c.Phase() != ReadsSwitched {
		return fmt.Errorf("%w: solo se vuelve atrás desde reads_switched (actual %s)", ErrPrecondition, c.Phase())
	}
	return c.set(ctx, Verified)
}

// Contract borra la columna vieja. Es el punto sin retorno: exige lecturas
// ya cambiadas, drift 0 y que ninguna ruta pueda seguir llegando al legado
// (legacyReaders es la cantidad de rutas que todavía leen phone en el legado).
func (c *Controller) Contract(ctx context.Context, legacyReaders int) error {
	if err := c.require(ReadsSwitched); err != nil {
		return err
	}
	if c.Phase() >= Contracted {
		return nil
	}
	if legacyReaders > 0 {
		return fmt.Errorf("%w: %d ruta(s) todavía leen customers.phone en el legado", ErrPrecondition, legacyReaders)
	}
	rep, err := c.verify(ctx)
	if err != nil {
		return err
	}
	if rep.Drift != 0 {
		return fmt.Errorf("%w: drift=%d", ErrPrecondition, rep.Drift)
	}
	if _, err := c.db.ExecContext(ctx, `ALTER TABLE customers DROP COLUMN phone`); err != nil {
		return err
	}
	return c.set(ctx, Contracted)
}

func hasColumn(ctx context.Context, db *store.DB, table, col string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, col).Scan(&n)
	return n > 0, err
}
