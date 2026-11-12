# Migración expand/contract medida

Generado por `scripts/migracion-medida.sh`. Dataset del benchmark (800 clientes, semilla 42), lotes de 200, un proceso por paso.
La duración incluye arrancar el proceso y abrir la base. Una sola corrida: sirve como orden de magnitud.

| Paso | Fase resultante | Duración (ms) | Reporte |
|---|---|---|---|
| status | none | 7 | `"-"` |
| expand | expanded | 77 | `"-"` |
| dual-write | dual_write | 7 | `"-"` |
| backfill | backfilled | 16 | `{"Scanned":800,"Updated":718,"Quarantined":82,"Batches":4}` |
| verify | verified | 12 | `{"rows":800,"drift":0,"quarantined":82}` |
| backfill | verified | 7 | `{"Scanned":800,"Updated":0,"Quarantined":82,"Batches":4}` |
| verify | verified | 5 | `{"rows":800,"drift":0,"quarantined":82}` |
| switch-reads | reads_switched | 8 | `"-"` |
| contract | contracted | 9 | `"-"` |
