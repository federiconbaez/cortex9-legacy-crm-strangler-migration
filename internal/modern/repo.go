package modern

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/migration"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/phone"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
)

// ErrNotFound indica que la entidad no existe.
var ErrNotFound = errors.New("modern: no encontrado")

// PhaseSource informa la fase de la migración del teléfono.
type PhaseSource interface{ Phase() migration.Phase }

// Repo concentra todo el SQL del servicio nuevo.
type Repo struct {
	db    *store.DB
	phase PhaseSource
}

// NewRepo crea el repositorio.
func NewRepo(db *store.DB, phase PhaseSource) *Repo { return &Repo{db: db, phase: phase} }

func parseTime(s string) time.Time {
	t, _ := time.Parse(seed.TimeLayout, s)
	return t
}

// Customer trae un cliente. Antes de reads_switched lee la columna vieja;
// después, la normalizada (y la cuarentena para los inválidos).
func (r *Repo) Customer(ctx context.Context, id int) (Customer, error) {
	c := Customer{ID: id}
	var created string
	var err error
	if r.phase.Phase() < migration.ReadsSwitched {
		var p sql.NullString
		err = r.db.QueryRowContext(ctx, `SELECT first_name, last_name, email, phone, segment, created_at FROM customers WHERE id = ?`, id).
			Scan(&c.FirstName, &c.LastName, &c.Email, &p, &c.Segment, &created)
		c.Phone = p.String
	} else {
		var e164, raw sql.NullString
		err = r.db.QueryRowContext(ctx, `SELECT c.first_name, c.last_name, c.email, c.phone_e164, q.raw, c.segment, c.created_at
			FROM customers c LEFT JOIN customer_phone_quarantine q ON q.customer_id = c.id WHERE c.id = ?`, id).
			Scan(&c.FirstName, &c.LastName, &c.Email, &e164, &raw, &c.Segment, &created)
		if e164.Valid {
			c.Phone = phone.Display(e164.String)
		} else {
			c.Phone = raw.String
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	c.CreatedAt = parseTime(created)
	return c, err
}

// TicketsWithInteractions trae los tickets de un cliente con sus
// interacciones en dos consultas (en vez de 1 + N + N*M).
func (r *Repo) TicketsWithInteractions(ctx context.Context, customerID int) ([]Ticket, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT t.id, t.subject, t.status, t.priority, t.created_at, COALESCE(a.name, '')
		FROM tickets t LEFT JOIN agents a ON a.id = t.agent_id
		WHERE t.customer_id = ? ORDER BY t.id`, customerID)
	if err != nil {
		return nil, err
	}
	var tickets []Ticket
	index := map[int]int{}
	for rows.Next() {
		t := Ticket{CustomerID: customerID}
		var created string
		if err := rows.Scan(&t.ID, &t.Subject, &t.Status, &t.Priority, &created, &t.AgentName); err != nil {
			rows.Close()
			return nil, err
		}
		t.CreatedAt = parseTime(created)
		index[t.ID] = len(tickets)
		tickets = append(tickets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(tickets) == 0 {
		return tickets, err
	}
	irows, err := r.db.QueryContext(ctx, `SELECT i.ticket_id, i.id, i.agent_id IS NOT NULL, COALESCE(a.name, ''), i.channel, i.body, i.created_at
		FROM interactions i JOIN tickets t ON t.id = i.ticket_id LEFT JOIN agents a ON a.id = i.agent_id
		WHERE t.customer_id = ? ORDER BY i.ticket_id, i.id`, customerID)
	if err != nil {
		return nil, err
	}
	defer irows.Close()
	for irows.Next() {
		var i Interaction
		var at string
		if err := irows.Scan(&i.TicketID, &i.ID, &i.ByAgent, &i.AgentName, &i.Channel, &i.Body, &at); err != nil {
			return nil, err
		}
		i.At = parseTime(at)
		k := index[i.TicketID]
		tickets[k].Interactions = append(tickets[k].Interactions, i)
	}
	return tickets, irows.Err()
}

// TicketPage es el listado en UNA consulta. Conserva el orden del legado
// (fecha formateada dd/mm/aaaa comparada como texto, desempate por id) porque
// hoy hay clientes de la API que dependen de él; ver ADR-0003.
func (r *Repo) TicketPage(ctx context.Context, status string, limit int) ([]Ticket, error) {
	rows, err := r.db.QueryContext(ctx, `WITH page AS (
			SELECT id FROM tickets WHERE (? = '' OR status = ?)
			ORDER BY strftime('%d/%m/%Y %H:%M', created_at) DESC, id ASC LIMIT ?)
		SELECT t.id, t.subject, t.status, t.priority, t.created_at, c.first_name, c.last_name, COALESCE(a.name, ''),
			(SELECT COUNT(*) FROM interactions i WHERE i.ticket_id = t.id)
		FROM page p JOIN tickets t ON t.id = p.id
		JOIN customers c ON c.id = t.customer_id
		LEFT JOIN agents a ON a.id = t.agent_id
		ORDER BY strftime('%d/%m/%Y %H:%M', t.created_at) DESC, t.id ASC`, status, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		var created string
		if err := rows.Scan(&t.ID, &t.Subject, &t.Status, &t.Priority, &created, &t.CustomerFirst, &t.CustomerLast, &t.AgentName, &t.InteractionCount); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdatePhone escribe el teléfono según la fase: solo la columna vieja,
// ambas (dual-write, en una transacción) o solo la nueva tras el contract.
// Igual que el legado, no informa si el cliente no existe.
func (r *Repo) UpdatePhone(ctx context.Context, id int, raw string) error {
	p := r.phase.Phase()
	if p < migration.DualWrite {
		_, err := r.db.ExecContext(ctx, `UPDATE customers SET phone = ? WHERE id = ?`, raw, id)
		return err
	}
	tx, err := r.db.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var e164 any
	if n, err := phone.Normalize(raw); err == nil {
		e164 = n
	}
	if p < migration.Contracted {
		_, err = tx.ExecContext(ctx, `UPDATE customers SET phone = ?, phone_e164 = ? WHERE id = ?`, raw, e164, id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE customers SET phone_e164 = ? WHERE id = ?`, e164, id)
	}
	if err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM customers WHERE id = ?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		if e164 == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO customer_phone_quarantine(customer_id, raw) VALUES(?, ?) ON CONFLICT(customer_id) DO UPDATE SET raw = excluded.raw`, id, raw)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM customer_phone_quarantine WHERE customer_id = ?`, id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
