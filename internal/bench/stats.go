// Package bench mide latencia, throughput y consultas por request de cada
// escenario del caso de estudio, siempre con los mismos datos y requests.
package bench

import (
	"math"
	"sort"
	"time"
)

// Latency resume una distribución de latencias en milisegundos.
type Latency struct {
	P50  float64 `json:"p50_ms"`
	P95  float64 `json:"p95_ms"`
	P99  float64 `json:"p99_ms"`
	Max  float64 `json:"max_ms"`
	Mean float64 `json:"mean_ms"`
}

// Percentile usa el método nearest-rank sobre datos ya ordenados.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func ms(d time.Duration) float64 { return math.Round(float64(d.Microseconds())/10) / 100 }

// Summarize calcula percentiles (ordena una copia).
func Summarize(samples []time.Duration) Latency {
	s := append([]time.Duration(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	l := Latency{P50: ms(Percentile(s, 50)), P95: ms(Percentile(s, 95)), P99: ms(Percentile(s, 99))}
	if len(s) > 0 {
		l.Max = ms(s[len(s)-1])
		l.Mean = ms(sum / time.Duration(len(s)))
	}
	return l
}
