package characterization

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

// Estos tests nombran explícitamente las rarezas que el golden ya fija, para
// que nadie las "arregle" sin darse cuenta de que rompe un contrato. Corren
// contra el legado y contra el nuevo.

func targets(t *testing.T) map[string]func() http.Handler {
	return map[string]func() http.Handler{
		"legacy": func() http.Handler { newEnv(t); return legacy.Handler() },
		"modern": func() http.Handler { return newEnv(t).Modern.Handler() },
	}
}

func getJSON(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	code, body := do(t, h, step{Method: "GET", Path: path})
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%s: %v (%q)", path, err, body)
	}
	return code, m
}

func TestQuirkNotFoundIs200(t *testing.T) {
	for name, mk := range targets(t) {
		t.Run(name, func(t *testing.T) {
			code, m := getJSON(t, mk(), "/customers/999/360")
			if code != 200 || m["error"] != "not found" {
				t.Fatalf("esperaba 200 + not found, vino %d %v", code, m)
			}
		})
	}
}

func TestQuirkNullCollections(t *testing.T) {
	for name, mk := range targets(t) {
		t.Run(name, func(t *testing.T) {
			h := mk()
			_, m := getJSON(t, h, "/customers/29/360")
			if v, ok := m["tickets"]; !ok || v != nil {
				t.Fatalf("tickets debería ser null, es %v", v)
			}
			// Un ticket sin interacciones: "interactions": null.
			if _, err := legacy.DB.Exec(`INSERT INTO tickets(id,customer_id,agent_id,subject,status,priority,created_at) VALUES(9001,29,NULL,'x','open',2,'2026-05-05T10:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			_, m = getJSON(t, h, "/customers/29/360")
			ts := m["tickets"].([]any)
			if len(ts) != 1 || ts[0].(map[string]any)["interactions"] != nil {
				t.Fatalf("interactions debería ser null: %v", ts)
			}
		})
	}
}

func TestQuirkTicketOrderIsLexicographicDDMM(t *testing.T) {
	for name, mk := range targets(t) {
		t.Run(name, func(t *testing.T) {
			_, m := getJSON(t, mk(), "/tickets?limit=100")
			var created []string
			var times []time.Time
			for _, it := range m["items"].([]any) {
				c := it.(map[string]any)["created"].(string)
				created = append(created, c)
				tm, _ := time.Parse("02/01/2006 15:04", c)
				times = append(times, tm)
			}
			if !sort.SliceIsSorted(created, func(i, j int) bool { return created[i] > created[j] }) {
				t.Fatal("el orden debería ser descendente por texto dd/mm/aaaa")
			}
			if sort.SliceIsSorted(times, func(i, j int) bool { return times[i].After(times[j]) }) {
				t.Fatal("si quedó ordenado cronológicamente, alguien cambió el comportamiento")
			}
		})
	}
}

func TestQuirkUnknownPriorityAndLimitCap(t *testing.T) {
	for name, mk := range targets(t) {
		t.Run(name, func(t *testing.T) {
			_, m := getJSON(t, mk(), "/tickets?limit=1000")
			items := m["items"].([]any)
			if len(items) != 100 || m["count"].(float64) != 100 {
				t.Fatalf("limit debería topearse en 100, vino %d", len(items))
			}
			empty := 0
			for _, it := range items {
				if it.(map[string]any)["priority"] == "" {
					empty++
				}
			}
			if empty == 0 {
				t.Fatal("esperaba al menos una prioridad fuera de catálogo mostrada como \"\"")
			}
		})
	}
}
