package modern

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
)

// Service une repositorio y reglas de dominio.
type Service struct {
	repo *Repo
	log  *slog.Logger
}

// NewService crea el servicio.
func NewService(repo *Repo, log *slog.Logger) *Service { return &Service{repo: repo, log: log} }

// Patrones de las rutas que el servicio nuevo sabe atender.
const (
	PatternCustomer    = "GET /customers/{id}"
	PatternCustomer360 = "GET /customers/{id}/360"
	PatternTickets     = "GET /tickets"
	PatternPhone       = "PUT /customers/{id}/phone"
)

// Handler devuelve el router del servicio nuevo.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PatternCustomer, s.getCustomer)
	mux.HandleFunc(PatternCustomer360, s.customer360)
	mux.HandleFunc(PatternTickets, s.listTickets)
	mux.HandleFunc(PatternPhone, s.updatePhone)
	return mux
}

// pathID reproduce al legado: id inválido -> 0 -> "no encontrado".
func pathID(r *http.Request) int {
	id, _ := strconv.Atoi(r.PathValue("id"))
	return id
}

func (s *Service) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Error("encode", "err", err)
	}
}

// QUIRK: "no encontrado" responde 200 con {"error":"not found"}.
func (s *Service) notFound(w http.ResponseWriter) {
	s.writeJSON(w, http.StatusOK, map[string]string{"error": "not found"})
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
}

func (s *Service) getCustomer(w http.ResponseWriter, r *http.Request) {
	c, err := s.repo.Customer(r.Context(), pathID(r))
	switch {
	case errors.Is(err, ErrNotFound):
		s.notFound(w)
	case err != nil:
		s.fail(w, r, err)
	default:
		s.writeJSON(w, http.StatusOK, presentCustomer(c))
	}
}

func (s *Service) customer360(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	c, err := s.repo.Customer(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tickets, err := s.repo.TicketsWithInteractions(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, present360(c, tickets, ComputeStats(tickets)))
}

func (s *Service) listTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, perr := strconv.Atoi(q.Get("limit"))
	ts, err := s.repo.TicketPage(r.Context(), q.Get("status"), ClampLimit(limit, perr))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, presentList(ts))
}

func (s *Service) updatePhone(w http.ResponseWriter, r *http.Request) {
	// QUIRK: el legado decodificaba a map[string]string, así que cualquier
	// campo no-string (aunque no se use) da 400. Se conserva.
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	var body map[string]string
	if err != nil || json.Unmarshal(raw, &body) != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := s.repo.UpdatePhone(r.Context(), pathID(r), body["phone"]); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
