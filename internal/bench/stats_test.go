package bench

import (
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	var s []time.Duration
	for i := 1; i <= 100; i++ {
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	for p, want := range map[float64]time.Duration{50: 50 * time.Millisecond, 95: 95 * time.Millisecond, 99: 99 * time.Millisecond, 100: 100 * time.Millisecond, 0: time.Millisecond} {
		if got := Percentile(s, p); got != want {
			t.Errorf("p%.0f = %v, quiero %v", p, got, want)
		}
	}
	if Percentile(nil, 50) != 0 {
		t.Fatal("vacío")
	}
	l := Summarize([]time.Duration{3 * time.Millisecond, time.Millisecond, 2 * time.Millisecond})
	if l.P50 != 2 || l.Max != 3 || l.Mean != 2 {
		t.Fatalf("%+v", l)
	}
}
