// Package legacy es el monolito CRM "heredado".
//
// ATENCIÓN: este código es MALO A PROPÓSITO. Es el punto de partida del caso
// de estudio y no se debe "arreglar" acá: se estrangula desde afuera.
// La deuda que contiene, intencionalmente:
//
//   - Estado global: la conexión vive en la variable de paquete DB.
//   - Lógica de negocio, SQL y presentación mezcladas en los handlers.
//   - Consultas N+1 (y N+1 anidadas) en la vista 360 y en el listado.
//   - El listado trae TODA la tabla de tickets y filtra/ordena en memoria.
//   - Ordena fechas como strings "dd/mm/aaaa" (orden incorrecto, ver quirks).
//   - Errores tragados, 200 para "no encontrado", log con fmt.Println.
//   - Ningún test propio: el comportamiento se fija con tests de
//     caracterización en el paquete characterization.
package legacy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/store"
)

// DB es la conexión global del monolito. Sí, global.
var DB *store.DB

// Verbose activa el "logging" del legado (fmt.Println, sin estructura).
var Verbose = false

// Handler devuelve el router del monolito.
func Handler() http.Handler {
	return http.HandlerFunc(route)
}

func route(w http.ResponseWriter, r *http.Request) {
	if Verbose {
		fmt.Println("request", r.Method, r.URL.Path)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	w.Header().Set("Content-Type", "application/json")
	if len(parts) == 1 && parts[0] == "tickets" && r.Method == "GET" {
		listTickets(w, r)
		return
	}
	if len(parts) >= 2 && parts[0] == "customers" {
		id, _ := strconv.Atoi(parts[1]) // id inválido -> 0 -> "not found"
		if len(parts) == 2 && r.Method == "GET" {
			getCustomer(w, id)
			return
		}
		if len(parts) == 3 && parts[2] == "360" && r.Method == "GET" {
			customer360(w, id)
			return
		}
		if len(parts) == 3 && parts[2] == "phone" && r.Method == "PUT" {
			updatePhone(w, r, id)
			return
		}
	}
	w.WriteHeader(404)
	w.Write([]byte(`{"error":"no route"}`))
}

func fecha(s string) string {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		return s
	}
	return t.Format("02/01/2006 15:04")
}

func prioridad(p int) string {
	if p == 1 {
		return "alta"
	} else if p == 2 {
		return "media"
	} else if p == 3 {
		return "baja"
	}
	return ""
}

func getCustomer(w http.ResponseWriter, id int) {
	var fn, ln, email, seg, created string
	var phone sql.NullString
	err := DB.QueryRow("SELECT first_name,last_name,email,phone,segment,created_at FROM customers WHERE id = ?", id).Scan(&fn, &ln, &email, &phone, &seg, &created)
	if err != nil {
		w.Write([]byte(`{"error":"not found"}`)) // 200 OK, sí
		return
	}
	out := map[string]interface{}{
		"id":      id,
		"name":    strings.ToUpper(ln) + ", " + fn,
		"email":   email,
		"phone":   phone.String,
		"segment": seg,
		"created": fecha(created),
	}
	json.NewEncoder(w).Encode(out)
}

func customer360(w http.ResponseWriter, id int) {
	var fn, ln, email, seg, created string
	var phone sql.NullString
	err := DB.QueryRow("SELECT first_name,last_name,email,phone,segment,created_at FROM customers WHERE id = ?", id).Scan(&fn, &ln, &email, &phone, &seg, &created)
	if err != nil {
		w.Write([]byte(`{"error":"not found"}`))
		return
	}
	customer := map[string]interface{}{
		"id": id, "name": strings.ToUpper(ln) + ", " + fn, "email": email,
		"phone": phone.String, "segment": seg, "created": fecha(created),
	}
	var tickets []map[string]interface{}
	total, open, totalInteractions, responded, sumMinutes := 0, 0, 0, 0, 0
	rows, err := DB.Query("SELECT id,subject,status,priority,created_at,agent_id FROM tickets WHERE customer_id = ?", id)
	if err == nil {
		type trow struct {
			id, prio          int
			subj, st, created string
			agent             sql.NullInt64
		}
		var trs []trow
		for rows.Next() {
			var t trow
			rows.Scan(&t.id, &t.subj, &t.st, &t.prio, &t.created, &t.agent)
			trs = append(trs, t)
		}
		rows.Close()
		for _, t := range trs {
			total++
			if t.st == "open" || t.st == "pending" {
				open++
			}
			agentName := ""
			if t.agent.Valid {
				DB.QueryRow("SELECT name FROM agents WHERE id = ?", t.agent.Int64).Scan(&agentName) // N+1
			}
			var inters []map[string]interface{}
			irows, err := DB.Query("SELECT id,agent_id,channel,body,created_at FROM interactions WHERE ticket_id = ?", t.id) // N+1
			if err == nil {
				type irow struct {
					id        int
					agent     sql.NullInt64
					ch, b, at string
				}
				var irs []irow
				for irows.Next() {
					var i irow
					irows.Scan(&i.id, &i.agent, &i.ch, &i.b, &i.at)
					irs = append(irs, i)
				}
				irows.Close()
				firstDone := false
				for _, i := range irs {
					totalInteractions++
					an := ""
					if i.agent.Valid {
						DB.QueryRow("SELECT name FROM agents WHERE id = ?", i.agent.Int64).Scan(&an) // N+1 anidado
						if !firstDone {
							t1, _ := time.Parse("2006-01-02T15:04:05Z", t.created)
							t2, _ := time.Parse("2006-01-02T15:04:05Z", i.at)
							sumMinutes += int(t2.Sub(t1).Minutes())
							responded++
							firstDone = true
						}
					}
					inters = append(inters, map[string]interface{}{"id": i.id, "channel": i.ch, "agent": an, "body": i.b, "at": fecha(i.at)})
				}
			}
			tickets = append(tickets, map[string]interface{}{
				"id": t.id, "subject": t.subj, "status": t.st, "priority": prioridad(t.prio),
				"created": fecha(t.created), "agent": agentName, "interactions": inters,
			})
		}
	}
	avg := 0
	if responded > 0 {
		avg = sumMinutes / responded // división entera: trunca
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"customer": customer,
		"tickets":  tickets, // nil si no hay tickets -> "null" en JSON
		"stats": map[string]interface{}{
			"total_tickets": total, "open_tickets": open,
			"interactions": totalInteractions, "avg_first_response_minutes": avg,
		},
	})
}

func listTickets(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := DB.Query("SELECT * FROM tickets") // toda la tabla
	if err != nil {
		w.WriteHeader(500)
		return
	}
	type t struct {
		id, cust, prio          int
		agent                   sql.NullInt64
		subj, st, created, disp string
	}
	var all []t
	for rows.Next() {
		var x t
		rows.Scan(&x.id, &x.cust, &x.agent, &x.subj, &x.st, &x.prio, &x.created)
		if status == "" || x.st == status {
			x.disp = fecha(x.created)
			all = append(all, x)
		}
	}
	rows.Close()
	// "más recientes primero"... comparando strings dd/mm/aaaa.
	sort.SliceStable(all, func(i, j int) bool { return all[i].disp > all[j].disp })
	if len(all) > limit {
		all = all[:limit]
	}
	items := []map[string]interface{}{}
	for _, x := range all {
		var fn, ln string
		DB.QueryRow("SELECT first_name,last_name FROM customers WHERE id = ?", x.cust).Scan(&fn, &ln) // N+1
		an := ""
		if x.agent.Valid {
			DB.QueryRow("SELECT name FROM agents WHERE id = ?", x.agent.Int64).Scan(&an) // N+1
		}
		n := 0
		DB.QueryRow("SELECT COUNT(*) FROM interactions WHERE ticket_id = ?", x.id).Scan(&n) // N+1 con full scan
		items = append(items, map[string]interface{}{
			"id": x.id, "subject": x.subj, "status": x.st, "priority": prioridad(x.prio),
			"created": x.disp, "customer": strings.ToUpper(ln) + ", " + fn, "agent": an, "interactions": n,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items, "count": len(items)})
}

func updatePhone(w http.ResponseWriter, r *http.Request, id int) {
	b, _ := io.ReadAll(r.Body)
	var body map[string]string
	if json.Unmarshal(b, &body) != nil {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"bad json"}`))
		return
	}
	DB.Exec("UPDATE customers SET phone = ? WHERE id = ?", body["phone"], id) // no mira filas afectadas
	w.Write([]byte(`{"ok":true}`))
}
