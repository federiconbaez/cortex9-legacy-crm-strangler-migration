// Package modern es el servicio que reemplaza al monolito, ruta por ruta.
//
// Capas:
//   - domain.go: tipos de dominio y reglas puras (testeables sin base).
//   - repo.go: SQL. Una consulta por colección, nunca una por fila.
//   - compat.go: el contrato JSON del legado, con sus rarezas aisladas en un
//     solo lugar para poder retirarlas de a una.
//   - http.go: handlers finos (parsear, llamar al servicio, presentar).
package modern

import "time"

// Customer es un cliente del CRM.
type Customer struct {
	ID        int
	FirstName string
	LastName  string
	Email     string
	Phone     string // lo que se muestra; su origen depende de la fase de migración
	Segment   string
	CreatedAt time.Time
}

// Ticket es un caso de atención.
type Ticket struct {
	ID           int
	CustomerID   int
	Subject      string
	Status       string
	Priority     int
	CreatedAt    time.Time
	AgentName    string
	Interactions []Interaction
	// InteractionCount se completa en el listado, donde no se traen las
	// interacciones completas.
	InteractionCount int
	CustomerFirst    string
	CustomerLast     string
}

// Interaction es un mensaje dentro de un ticket.
type Interaction struct {
	ID        int
	TicketID  int
	ByAgent   bool
	AgentName string
	Channel   string
	Body      string
	At        time.Time
}

// Stats son los indicadores de la vista 360.
type Stats struct {
	TotalTickets            int
	OpenTickets             int
	Interactions            int
	AvgFirstResponseMinutes int
}

// IsOpen dice si el estado cuenta como abierto para las estadísticas.
func IsOpen(status string) bool { return status == "open" || status == "pending" }

// ComputeStats calcula las estadísticas como lo hacía el legado: el primer
// "respuesta" es la primera interacción con agente (aunque el agente ya no
// exista), los minutos se truncan por ticket y el promedio usa división
// entera.
func ComputeStats(tickets []Ticket) Stats {
	var s Stats
	sum, responded := 0, 0
	for _, t := range tickets {
		s.TotalTickets++
		if IsOpen(t.Status) {
			s.OpenTickets++
		}
		s.Interactions += len(t.Interactions)
		for _, i := range t.Interactions {
			if i.ByAgent {
				sum += int(i.At.Sub(t.CreatedAt).Minutes())
				responded++
				break
			}
		}
	}
	if responded > 0 {
		s.AvgFirstResponseMinutes = sum / responded
	}
	return s
}

// ClampLimit reproduce la regla de paginado del legado.
func ClampLimit(limit int, parseErr error) int {
	if parseErr != nil || limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}
