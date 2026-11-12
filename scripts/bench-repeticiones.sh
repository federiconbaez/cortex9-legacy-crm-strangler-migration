#!/usr/bin/env bash
# Corre el benchmark N veces (por defecto 3) con los mismos datos y requests,
# deja la corrida 1 en results/{bench.json,summary.md}, guarda cada corrida en
# results/repeticiones/ y arma results/repeticiones.md con el rango por métrica.
# Una sola corrida en una máquina compartida no alcanza para afirmar nada.
set -euo pipefail
cd "$(dirname "$0")/.."
n="${1:-3}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p results/repeticiones
rm -f results/repeticiones/*.json
for i in $(seq 1 "$n"); do
  echo "corrida $i de $n" >&2
  go run ./cmd/bench -out "$tmp/rep$i" 2> "$tmp/rep$i.log"
  cp "$tmp/rep$i/bench.json" "results/repeticiones/rep$i.json"
done
cp "$tmp/rep1/bench.json" results/bench.json
cp "$tmp/rep1/summary.md" results/summary.md

{
  echo "# Repeticiones del benchmark"
  echo
  echo "Generado por \`scripts/bench-repeticiones.sh $n\`. Cada celda es \`mín–máx\` de las $n corridas (mismo dataset, mismas requests)."
  echo "Las consultas por request no dependen de la máquina: son idénticas en todas las corridas."
  echo
  echo "| Escenario | Endpoint | p50 ms | p95 ms | p99 ms | rps | consultas/req |"
  echo "|---|---|---|---|---|---|---|"
  jq -rs '
    def r: (.*100|round/100);
    def rng(f): (map(f)|min|r|tostring) + "–" + (map(f)|max|r|tostring);
    [ .[] | .scenarios[] as $s | $s.endpoints[] | {s:$s.name, e:.endpoint, p50:.latency.p50_ms, p95:.latency.p95_ms, p99:.latency.p99_ms, rps:.throughput_rps, q:.queries_per_request} ]
    | group_by([.s,.e]) | sort_by(.[0].s as $n | ["baseline-legacy","legacy-con-indices","modern-sin-indices","fachada-legacy","fachada-shadow","final-modern"] | index($n))
    | .[] | "| \(.[0].s) | \(.[0].e) | \(rng(.p50)) | \(rng(.p95)) | \(rng(.p99)) | \(rng(.rps)) | \(rng(.q)) |"
  ' results/repeticiones/rep*.json
} > results/repeticiones.md
cat results/repeticiones.md
