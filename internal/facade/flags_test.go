package facade

import (
	"strconv"
	"strings"
	"testing"
)

func TestBucketDistributionIsRoughlyUniform(t *testing.T) {
	const n = 20000
	in := 0
	for i := 0; i < n; i++ {
		if (RouteFlag{Mode: ModeCanary, Percent: 10}).UseModern("customer_360", strconv.Itoa(i)) {
			in++
		}
	}
	got := float64(in) / n
	if got < 0.09 || got > 0.11 {
		t.Fatalf("10%% pedido, %.2f%% obtenido", got*100)
	}
}

func TestRolloutIsStickyAndMonotonic(t *testing.T) {
	for i := 0; i < 2000; i++ {
		k := strconv.Itoa(i)
		prev := false
		for p := 0; p <= 100; p += 5 {
			now := (RouteFlag{Mode: ModeCanary, Percent: p}).UseModern("r", k)
			if prev && !now {
				t.Fatalf("clave %s salió del rollout al subir a %d%%", k, p)
			}
			if again := (RouteFlag{Mode: ModeCanary, Percent: p}).UseModern("r", k); again != now {
				t.Fatal("la decisión no es estable")
			}
			prev = now
		}
		if !prev {
			t.Fatal("al 100% entran todos")
		}
	}
	if (RouteFlag{Mode: ModeCanary, Percent: 0}).UseModern("r", "x") {
		t.Fatal("al 0% no entra nadie")
	}
}

func TestBucketDependsOnRoute(t *testing.T) {
	same := 0
	for i := 0; i < 1000; i++ {
		k := strconv.Itoa(i)
		if Bucket("a", k) == Bucket("b", k) {
			same++
		}
	}
	if same > 50 {
		t.Fatalf("las rutas comparten baldes demasiado seguido: %d/1000", same)
	}
}

func TestValidateAndLoad(t *testing.T) {
	bad := []RouteFlag{{Mode: "x"}, {Mode: ModeCanary, Percent: 101}, {Mode: ModeCanary, Percent: -1}, {Mode: ModeModern, Percent: 5}}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("%+v debería ser inválido", b)
		}
	}
	f := NewFlags()
	if f.Get("x").Mode != ModeLegacy {
		t.Fatal("por defecto legacy")
	}
	if err := f.Load(strings.NewReader(`{"a":{"mode":"canary","percent":30},"b":{"mode":"nope"}}`)); err == nil {
		t.Fatal("debería rechazar todo el archivo")
	}
	if f.Get("a").Mode != ModeLegacy {
		t.Fatal("un archivo inválido no debe aplicarse a medias")
	}
	if err := f.Load(strings.NewReader(`{"a":{"mode":"canary","percent":30,"shadow":true}}`)); err != nil {
		t.Fatal(err)
	}
	if got := f.Get("a"); got.Percent != 30 || !got.Shadow {
		t.Fatalf("%+v", got)
	}
	if err := f.Load(strings.NewReader(`{"a":{"mode":"modern","typo":1}}`)); err == nil {
		t.Fatal("campos desconocidos deberían fallar")
	}
}
