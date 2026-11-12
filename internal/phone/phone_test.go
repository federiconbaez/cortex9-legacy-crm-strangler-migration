package phone

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"+54 11 4567-8901", "+541145678901", true},
		{"011 4567-8901", "+541145678901", true},
		{"(11) 4567 8901", "+541145678901", true},
		{"1145678901", "+541145678901", true},
		{"+54 9 11 4567-8901", "+5491145678901", true},
		{"15-4567-8901", "", false},
		{"", "", false},
		{"abc", "", false},
		{"+54 11 4567-890", "", false},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; quiero %q ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestDisplayRoundTrip(t *testing.T) {
	for in, want := range map[string]string{
		"+541145678901":  "+54 11 4567-8901",
		"+5491145678901": "+54 9 11 4567-8901",
		"+1555":          "+1555",
	} {
		if got := Display(in); got != want {
			t.Errorf("Display(%q) = %q; quiero %q", in, got, want)
		}
		if n, err := Normalize(Display(in)); err == nil && n != in {
			t.Errorf("ida y vuelta de %q dio %q", in, n)
		}
	}
}

func FuzzNormalizeIdempotent(f *testing.F) {
	f.Add("+54 11 4567-8901")
	f.Add("15-1234-5678")
	f.Fuzz(func(t *testing.T, s string) {
		n, err := Normalize(s)
		if err != nil {
			return
		}
		n2, err := Normalize(n)
		if err != nil || n2 != n {
			t.Fatalf("no idempotente: %q -> %q -> %q (%v)", s, n, n2, err)
		}
	})
}
