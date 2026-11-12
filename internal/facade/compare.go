package facade

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// Diff es una diferencia entre la respuesta del legado y la del nuevo.
type Diff struct {
	Path   string `json:"path"`
	Legacy string `json:"legacy"`
	Modern string `json:"modern"`
}

// Rule marca una diferencia como aceptada (conocida y decidida), con motivo.
type Rule struct {
	Path   string `json:"path"` // ruta normalizada, p. ej. "customer.phone"
	Reason string `json:"reason"`
}

var indexRe = regexp.MustCompile(`\[\d+\]`)

// NormalizePath reemplaza índices por [] para agrupar ("tickets[3].id" -> "tickets[].id").
func NormalizePath(p string) string { return indexRe.ReplaceAllString(p, "[]") }

func render(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// CompareJSON compara semánticamente dos cuerpos JSON: el orden de claves no
// importa, el orden de los arrays sí, y null es distinto de [].
func CompareJSON(legacy, modern []byte) ([]Diff, error) {
	var a, b any
	if err := decode(legacy, &a); err != nil {
		return nil, fmt.Errorf("legado: %w", err)
	}
	if err := decode(modern, &b); err != nil {
		return nil, fmt.Errorf("nuevo: %w", err)
	}
	var out []Diff
	walk("", a, b, &out)
	return out, nil
}

func decode(data []byte, v *any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		*v = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // evita que 1 y 1.0000000000000001 parezcan iguales
	return dec.Decode(v)
}

func join(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func walk(path string, a, b any, out *[]Diff) {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, Diff{Path: path, Legacy: render(a), Modern: render(b)})
			return
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			x, okA := av[k]
			y, okB := bv[k]
			switch {
			case !okA:
				*out = append(*out, Diff{Path: join(path, k), Legacy: "<ausente>", Modern: render(y)})
			case !okB:
				*out = append(*out, Diff{Path: join(path, k), Legacy: render(x), Modern: "<ausente>"})
			default:
				walk(join(path, k), x, y, out)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			*out = append(*out, Diff{Path: path, Legacy: render(a), Modern: render(b)})
			return
		}
		if len(av) != len(bv) {
			*out = append(*out, Diff{Path: path + ".length", Legacy: fmt.Sprint(len(av)), Modern: fmt.Sprint(len(bv))})
		}
		n := min(len(av), len(bv))
		for i := 0; i < n; i++ {
			walk(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i], out)
		}
	default:
		if render(a) != render(b) {
			*out = append(*out, Diff{Path: path, Legacy: render(a), Modern: render(b)})
		}
	}
}

// Classify separa diferencias aceptadas de las que cuentan como mismatch.
func Classify(diffs []Diff, rules []Rule) (mismatch, accepted []Diff) {
	allowed := map[string]bool{}
	for _, r := range rules {
		allowed[r.Path] = true
	}
	for _, d := range diffs {
		if allowed[NormalizePath(d.Path)] {
			accepted = append(accepted, d)
		} else {
			mismatch = append(mismatch, d)
		}
	}
	return mismatch, accepted
}
