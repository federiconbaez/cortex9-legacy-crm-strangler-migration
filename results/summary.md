# Resultados del benchmark

Generado por `go run ./cmd/bench` el 2026-11-12T11:34:45Z. No editar a mano: se regenera.

- Máquina: Intel(R) Xeon(R) Processor, 8 CPU, linux/amd64, go1.27.3
- Base: modernc.org/sqlite (Go puro), WAL, archivo local, 8 conexiones
- Dataset: 800 clientes, 30 agentes, 6000 tickets, 30000 interacciones (semilla 42)
- Por endpoint y escenario: 20 de calentamiento, 20 en serie para contar consultas, 300 medidas con 4 clientes concurrentes
- Latencia medida en el cliente HTTP (loopback, keep-alive), percentiles nearest-rank. Una sola corrida: ver caveats en README.

## Línea base vs. estado final

| Endpoint | p50 base → final (ms) | p95 base → final (ms) | p99 base → final (ms) | rps base → final | consultas/req base → final | mejora p95 |
|---|---|---|---|---|---|---|
| customer_get | 0.07 → 0.08 | 0.22 → 0.24 | 0.57 → 0.69 | 42222.8 → 34924.6 | 1.00 → 1.00 | 0.9x |
| customer_360 | 11.39 → 0.70 | 21.01 → 1.39 | 42.94 → 1.95 | 331.1 → 5244.4 | 35.80 → 2.80 | 15.1x |
| ticket_list | 77.26 → 13.20 | 102.43 → 36.97 | 146.41 → 39.16 | 48.9 → 220.7 | 142.25 → 1.00 | 2.8x |

## Todos los escenarios

### customer_get

| Escenario | p50 ms | p95 ms | p99 ms | max ms | rps | consultas/req (min–max) | errores |
|---|---|---|---|---|---|---|---|
| baseline-legacy | 0.07 | 0.22 | 0.57 | 0.76 | 42222.8 | 1.00 (1–1) | 0 |
| legacy-con-indices | 0.07 | 0.17 | 0.79 | 1.11 | 40023.7 | 1.00 (1–1) | 0 |
| modern-sin-indices | 0.07 | 0.18 | 0.42 | 0.61 | 45424.9 | 1.00 (1–1) | 0 |
| fachada-legacy | 0.07 | 0.18 | 0.43 | 0.57 | 44440.8 | 1.00 (1–1) | 0 |
| fachada-shadow | 0.08 | 0.39 | 0.83 | 0.95 | 33260.6 | 2.00 (2–2) (incluye sombra) | 0 |
| final-modern | 0.08 | 0.24 | 0.69 | 1.20 | 34924.6 | 1.00 (1–1) | 0 |

### customer_360

| Escenario | p50 ms | p95 ms | p99 ms | max ms | rps | consultas/req (min–max) | errores |
|---|---|---|---|---|---|---|---|
| baseline-legacy | 11.39 | 21.01 | 42.94 | 109.96 | 331.1 | 35.80 (2–65) | 0 |
| legacy-con-indices | 1.04 | 2.06 | 2.92 | 3.30 | 3678.0 | 35.80 (2–65) | 0 |
| modern-sin-indices | 67.31 | 72.83 | 90.03 | 110.26 | 64.8 | 2.80 (2–3) | 0 |
| fachada-legacy | 11.23 | 18.93 | 21.83 | 25.54 | 363.0 | 35.80 (2–65) | 0 |
| fachada-shadow | 1.72 | 3.34 | 4.06 | 4.41 | 2233.2 | 38.60 (4–68) (incluye sombra) | 0 |
| final-modern | 0.70 | 1.39 | 1.95 | 2.24 | 5244.4 | 2.80 (2–3) | 0 |

### ticket_list

| Escenario | p50 ms | p95 ms | p99 ms | max ms | rps | consultas/req (min–max) | errores |
|---|---|---|---|---|---|---|---|
| baseline-legacy | 77.26 | 102.43 | 146.41 | 166.68 | 48.9 | 142.25 (137–145) | 0 |
| legacy-con-indices | 14.93 | 22.96 | 27.21 | 30.59 | 250.1 | 142.25 (137–145) | 0 |
| modern-sin-indices | 73.53 | 89.75 | 111.21 | 116.84 | 52.6 | 1.00 (1–1) | 0 |
| fachada-legacy | 76.62 | 86.91 | 96.41 | 99.99 | 51.0 | 142.25 (137–145) | 0 |
| fachada-shadow | 16.27 | 24.14 | 27.93 | 33.01 | 228.8 | 143.25 (138–146) (incluye sombra) | 0 |
| final-modern | 13.20 | 36.97 | 39.16 | 48.08 | 220.7 | 1.00 (1–1) | 0 |

## Escenarios

- **baseline-legacy** (fase `none`): Monolito legado directo, esquema legado (sin índices). LÍNEA BASE.
- **legacy-con-indices** (fase `none`): Mismo código legado + solo los índices del expand. Aísla el efecto de los índices.
- **modern-sin-indices** (fase `none`): Código nuevo (sin N+1) sobre el esquema legado sin índices. Aísla el efecto del código.
- **fachada-legacy** (fase `none`): Fachada strangler con todo en legacy. Mide el costo de la fachada.
- **fachada-shadow** (fase `verified`): Fachada en sombra (responde legacy, compara con modern) con la migración en verified.
- **final-modern** (fase `contracted`): Fachada con todo en modern, migración contraída (columna vieja borrada). ESTADO FINAL.

## Comparador en sombra (fachada-shadow)

| Ruta | en sombra | mismatches | solo aceptadas | descartadas | errores |
|---|---|---|---|---|---|
| customer_get | 340 | 0 | 0 | 0 | 0 |
| customer_360 | 340 | 0 | 0 | 0 | 0 |
| ticket_list | 340 | 0 | 0 | 0 | 0 |
