package facade

import "testing"

func TestCompareJSON(t *testing.T) {
	cases := []struct {
		name, a, b string
		want       []string
	}{
		{"iguales con otro orden de claves", `{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1}`, nil},
		{"valor distinto", `{"a":{"b":"x"}}`, `{"a":{"b":"y"}}`, []string{"a.b"}},
		{"null vs vacío", `{"t":null}`, `{"t":[]}`, []string{"t"}},
		{"orden de array importa", `{"t":[1,2]}`, `{"t":[2,1]}`, []string{"t[0]", "t[1]"}},
		{"largo distinto", `{"t":[1]}`, `{"t":[1,2]}`, []string{"t.length"}},
		{"campo ausente", `{"a":1,"b":2}`, `{"a":1}`, []string{"b"}},
		{"campo extra", `{"a":1}`, `{"a":1,"c":3}`, []string{"c"}},
		{"número vs string", `{"a":1}`, `{"a":"1"}`, []string{"a"}},
		{"precisión", `{"a":1}`, `{"a":1.0}`, []string{"a"}},
		{"array anidado", `{"t":[{"x":1},{"x":2}]}`, `{"t":[{"x":1},{"x":3}]}`, []string{"t[1].x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := CompareJSON([]byte(c.a), []byte(c.b))
			if err != nil {
				t.Fatal(err)
			}
			if len(d) != len(c.want) {
				t.Fatalf("diffs %+v, quiero %v", d, c.want)
			}
			for i := range d {
				if d[i].Path != c.want[i] {
					t.Errorf("diff %d: %s, quiero %s", i, d[i].Path, c.want[i])
				}
			}
		})
	}
	if _, err := CompareJSON([]byte(`{`), []byte(`{}`)); err == nil {
		t.Fatal("JSON inválido debería dar error")
	}
}

func TestClassify(t *testing.T) {
	diffs := []Diff{{Path: "customer.phone"}, {Path: "tickets[3].agent"}, {Path: "tickets[0].agent"}}
	mis, acc := Classify(diffs, []Rule{{Path: "customer.phone"}})
	if len(acc) != 1 || len(mis) != 2 {
		t.Fatalf("mis=%v acc=%v", mis, acc)
	}
	if NormalizePath("tickets[12].interactions[3].id") != "tickets[].interactions[].id" {
		t.Fatal(NormalizePath("tickets[12].interactions[3].id"))
	}
}
