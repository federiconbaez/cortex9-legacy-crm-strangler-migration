package modern_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/app"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/internal/seed"
	"github.com/federiconbaez/cortex9-legacy-crm-strangler-migration/legacy"
)

// TestQueryCountIsConstant demuestra el N+1: el legado hace más consultas
// cuantos más tickets e interacciones tiene el cliente; el nuevo, siempre las
// mismas.
func TestQueryCountIsConstant(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "crm.db")
	if err := app.Seed(ctx, path, seed.Small); err != nil {
		t.Fatal(err)
	}
	env, err := app.Open(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	measure := func(target, url string) int64 {
		h := legacy.Handler()
		if target == "modern" {
			h = env.Modern.Handler()
		}
		before := env.DB.Queries()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != 200 {
			t.Fatalf("%s %s: %d", target, url, rec.Code)
		}
		return env.DB.Queries() - before
	}
	seen := map[int64]bool{}
	for _, id := range []string{"1", "2", "5", "29"} {
		l := measure("legacy", "/customers/"+id+"/360")
		m := measure("modern", "/customers/"+id+"/360")
		seen[l] = true
		if id == "29" { // sin tickets: el nuevo corta antes
			if m != 2 {
				t.Errorf("modern 360 sin tickets: %d consultas, quiero 2", m)
			}
		} else if m != 3 {
			t.Errorf("modern 360 cliente %s: %d consultas, quiero 3", id, m)
		}
		if id != "29" && l <= m {
			t.Errorf("legacy 360 cliente %s: %d consultas; debería superar a modern (%d)", id, l, m)
		}
	}
	if len(seen) < 3 {
		t.Errorf("el legado debería variar con el cliente (N+1): %v", seen)
	}
	if l, m := measure("legacy", "/tickets?limit=50"), measure("modern", "/tickets?limit=50"); m != 1 || l < 100 {
		t.Errorf("listado: legacy=%d (quiero >=100) modern=%d (quiero 1)", l, m)
	}
}
