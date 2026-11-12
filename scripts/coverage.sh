#!/usr/bin/env bash
# Cobertura del código propio (internal/ y legacy/). -coverpkg hace que los
# tests de caracterización e integración cuenten para los paquetes que
# ejercitan (sin esa bandera, cada paquete solo mide sus propios tests).
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p results
pkgs=./internal/...,./legacy/...
go test -count=1 -coverpkg="$pkgs" -coverprofile=results/coverage.out ./... >/dev/null
{
  echo "# go test -coverpkg=$pkgs ./..."
  go tool cover -func=results/coverage.out | awk '/^total/ {print "total:", $NF}'
  echo
  echo "# por paquete: sentencias cubiertas / sentencias (con todos los tests del repo)"
  awk -F'[: ,]' 'NR > 1 {f=$1; sub(/\/[^\/]*$/,"",f); sub(/.*strangler-migration\//,"",f); key=$1":"$2","$3; if (!(key in seen)) {seen[key]=1; tot[f]+=$(NF-1)}; if ($NF > 0 && !(key in hit)) {hit[key]=1; cov[f]+=$(NF-1)}} END {for (k in tot) printf "%s %.1f%% (%d/%d)\n", k, 100*cov[k]/tot[k], cov[k], tot[k]}' results/coverage.out | sort
} > results/coverage.txt
rm -f results/coverage.out
cat results/coverage.txt
