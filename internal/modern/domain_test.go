package modern

import (
	"errors"
	"testing"
	"time"
)

func TestComputeStats(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	at := func(min float64) time.Time { return t0.Add(time.Duration(min * float64(time.Minute))) }
	tickets := []Ticket{
		{Status: "open", CreatedAt: t0, Interactions: []Interaction{
			{ByAgent: false, At: at(5)},
			{ByAgent: true, At: at(10.9)}, // trunca a 10
			{ByAgent: true, At: at(50)},   // no es la primera respuesta
		}},
		{Status: "pending", CreatedAt: t0, Interactions: []Interaction{{ByAgent: true, At: at(15)}}},
		{Status: "closed", CreatedAt: t0, Interactions: []Interaction{{ByAgent: false, At: at(1)}}}, // sin respuesta
		{Status: "closed", CreatedAt: t0},
	}
	got := ComputeStats(tickets)
	want := Stats{TotalTickets: 4, OpenTickets: 2, Interactions: 5, AvgFirstResponseMinutes: 12} // (10+15)/2 = 12 (entero)
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if (ComputeStats(nil) != Stats{}) {
		t.Fatal("sin tickets todo en cero")
	}
}

func TestClampLimit(t *testing.T) {
	bad := errors.New("x")
	for _, c := range []struct {
		in   int
		err  error
		want int
	}{{0, nil, 20}, {-1, nil, 20}, {5, nil, 5}, {100, nil, 100}, {101, nil, 100}, {50, bad, 20}} {
		if got := ClampLimit(c.in, c.err); got != c.want {
			t.Errorf("ClampLimit(%d,%v)=%d quiero %d", c.in, c.err, got, c.want)
		}
	}
}

func TestLegacyPresentation(t *testing.T) {
	if legacyName("Ana", "Núñez") != "NÚÑEZ, Ana" {
		t.Fatal(legacyName("Ana", "Núñez"))
	}
	for p, want := range map[int]string{1: "alta", 2: "media", 3: "baja", 4: "", 0: ""} {
		if legacyPriority(p) != want {
			t.Errorf("prioridad %d", p)
		}
	}
	out := present360(Customer{}, nil, Stats{})
	if out.Tickets != nil {
		t.Fatal("sin tickets debe ser nil (null en JSON)")
	}
	if l := presentList(nil); l.Items == nil || l.Count != 0 {
		t.Fatal("el listado vacío es [] y no null")
	}
}
