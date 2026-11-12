#!/usr/bin/env bash
# Corre la migración expand/contract completa sobre el dataset del benchmark
# con cmd/migrate (un proceso por paso, como se haría en producción) y escribe
# results/migracion.md con el reporte de cada paso y su duración.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/seed" ./cmd/seed
go build -o "$tmp/migrate" ./cmd/migrate
db="$tmp/crm.db"
"$tmp/seed" -db "$db" -size bench 2>/dev/null
{
  echo "# Migración expand/contract medida"
  echo
  echo "Generado por \`scripts/migracion-medida.sh\`. Dataset del benchmark (800 clientes, semilla 42), lotes de 200, un proceso por paso."
  echo "La duración incluye arrancar el proceso y abrir la base. Una sola corrida: sirve como orden de magnitud."
  echo
  echo "| Paso | Fase resultante | Duración (ms) | Reporte |"
  echo "|---|---|---|---|"
  for step in status expand dual-write backfill verify backfill verify switch-reads contract; do
    extra=()
    [ "$step" = contract ] && extra=(-legacy-readers 0)
    t0=$(date +%s%N)
    out="$("$tmp/migrate" -db "$db" -step "$step" -batch 200 "${extra[@]}" 2>&1)"
    t1=$(date +%s%N)
    phase=$(jq -r .phase <<<"$out")
    rep=$(jq -c '.report // "-"' <<<"$out")
    echo "| $step | $phase | $(( (t1 - t0) / 1000000 )) | \`$rep\` |"
  done
} > results/migracion.md
cat results/migracion.md
