package modern

import "strings"

// Este archivo es el contrato JSON heredado. Cada rareza está marcada con
// QUIRK para poder retirarla con un cambio de versión de API, no a escondidas.
// Los campos van en orden alfabético porque el legado serializaba maps (y
// encoding/json ordena las claves): así la salida coincide byte a byte.

const legacyDate = "02/01/2006 15:04"

// QUIRK: "APELLIDO, Nombre".
func legacyName(first, last string) string { return strings.ToUpper(last) + ", " + first }

// QUIRK: prioridad fuera de catálogo se muestra como "".
func legacyPriority(p int) string {
	switch p {
	case 1:
		return "alta"
	case 2:
		return "media"
	case 3:
		return "baja"
	}
	return ""
}

type customerJSON struct {
	Created string `json:"created"`
	Email   string `json:"email"`
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Segment string `json:"segment"`
}

type interactionJSON struct {
	Agent   string `json:"agent"`
	At      string `json:"at"`
	Body    string `json:"body"`
	Channel string `json:"channel"`
	ID      int    `json:"id"`
}

type ticket360JSON struct {
	Agent        string            `json:"agent"`
	Created      string            `json:"created"`
	ID           int               `json:"id"`
	Interactions []interactionJSON `json:"interactions"` // QUIRK: null si no hay
	Priority     string            `json:"priority"`
	Status       string            `json:"status"`
	Subject      string            `json:"subject"`
}

type statsJSON struct {
	AvgFirstResponseMinutes int `json:"avg_first_response_minutes"`
	Interactions            int `json:"interactions"`
	OpenTickets             int `json:"open_tickets"`
	TotalTickets            int `json:"total_tickets"`
}

type customer360JSON struct {
	Customer customerJSON    `json:"customer"`
	Stats    statsJSON       `json:"stats"`
	Tickets  []ticket360JSON `json:"tickets"` // QUIRK: null si no hay
}

type ticketItemJSON struct {
	Agent        string `json:"agent"`
	Created      string `json:"created"`
	Customer     string `json:"customer"`
	ID           int    `json:"id"`
	Interactions int    `json:"interactions"`
	Priority     string `json:"priority"`
	Status       string `json:"status"`
	Subject      string `json:"subject"`
}

type ticketListJSON struct {
	Count int              `json:"count"`
	Items []ticketItemJSON `json:"items"` // acá sí es [] y no null
}

func presentCustomer(c Customer) customerJSON {
	return customerJSON{
		Created: c.CreatedAt.Format(legacyDate), Email: c.Email, ID: c.ID,
		Name: legacyName(c.FirstName, c.LastName), Phone: c.Phone, Segment: c.Segment,
	}
}

func present360(c Customer, tickets []Ticket, s Stats) customer360JSON {
	out := customer360JSON{
		Customer: presentCustomer(c),
		Stats: statsJSON{
			AvgFirstResponseMinutes: s.AvgFirstResponseMinutes, Interactions: s.Interactions,
			OpenTickets: s.OpenTickets, TotalTickets: s.TotalTickets,
		},
	}
	for _, t := range tickets {
		tj := ticket360JSON{
			Agent: t.AgentName, Created: t.CreatedAt.Format(legacyDate), ID: t.ID,
			Priority: legacyPriority(t.Priority), Status: t.Status, Subject: t.Subject,
		}
		for _, i := range t.Interactions {
			tj.Interactions = append(tj.Interactions, interactionJSON{
				Agent: i.AgentName, At: i.At.Format(legacyDate), Body: i.Body, Channel: i.Channel, ID: i.ID,
			})
		}
		out.Tickets = append(out.Tickets, tj)
	}
	return out
}

func presentList(ts []Ticket) ticketListJSON {
	out := ticketListJSON{Items: []ticketItemJSON{}}
	for _, t := range ts {
		out.Items = append(out.Items, ticketItemJSON{
			Agent: t.AgentName, Created: t.CreatedAt.Format(legacyDate), Customer: legacyName(t.CustomerFirst, t.CustomerLast),
			ID: t.ID, Interactions: t.InteractionCount, Priority: legacyPriority(t.Priority), Status: t.Status, Subject: t.Subject,
		})
	}
	out.Count = len(out.Items)
	return out
}
