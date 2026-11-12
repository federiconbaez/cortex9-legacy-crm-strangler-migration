# Repeticiones del benchmark

Generado por `scripts/bench-repeticiones.sh 3`. Cada celda es `mín–máx` de las 3 corridas (mismo dataset, mismas requests).
Las consultas por request no dependen de la máquina: son idénticas en todas las corridas.

| Escenario | Endpoint | p50 ms | p95 ms | p99 ms | rps | consultas/req |
|---|---|---|---|---|---|---|
| baseline-legacy | customer_360 | 11.04–11.39 | 19.01–21.01 | 21.28–42.94 | 331.11–370.95 | 35.8–35.8 |
| baseline-legacy | customer_get | 0.06–0.07 | 0.2–0.29 | 0.57–0.62 | 40698.74–45814.7 | 1–1 |
| baseline-legacy | ticket_list | 74.81–77.26 | 85.84–102.43 | 89.83–146.41 | 48.93–51.91 | 142.25–142.25 |
| legacy-con-indices | customer_360 | 0.92–1.04 | 1.72–2.06 | 2–2.92 | 3677.98–4141.43 | 35.8–35.8 |
| legacy-con-indices | customer_get | 0.07–0.07 | 0.17–0.28 | 0.56–0.79 | 40023.66–45152.36 | 1–1 |
| legacy-con-indices | ticket_list | 13.49–14.93 | 20.53–22.96 | 24.52–68.6 | 250.05–272.94 | 142.25–142.25 |
| modern-sin-indices | customer_360 | 66.34–67.52 | 71.19–72.83 | 72.96–90.03 | 64.76–66.3 | 2.8–2.8 |
| modern-sin-indices | customer_get | 0.06–0.07 | 0.16–0.21 | 0.42–0.89 | 41617.62–45424.94 | 1–1 |
| modern-sin-indices | ticket_list | 71.92–73.53 | 78.08–89.75 | 84.8–111.21 | 52.64–54.56 | 1–1 |
| fachada-legacy | customer_360 | 11.23–11.43 | 18.93–20.85 | 21.83–24.55 | 341.84–363.01 | 35.8–35.8 |
| fachada-legacy | customer_get | 0.07–0.07 | 0.16–0.18 | 0.43–0.95 | 42312.49–44440.76 | 1–1 |
| fachada-legacy | ticket_list | 74.65–76.62 | 81.83–86.91 | 86.09–96.41 | 50.96–52.65 | 142.25–142.25 |
| fachada-shadow | customer_360 | 1.57–1.72 | 2.91–3.34 | 4.01–6.58 | 2233.2–2425.51 | 38.6–38.6 |
| fachada-shadow | customer_get | 0.08–0.09 | 0.36–0.43 | 0.74–0.89 | 31653.14–33260.57 | 2–2 |
| fachada-shadow | ticket_list | 15.79–16.27 | 23.16–24.14 | 25.28–27.93 | 228.82–239.68 | 143.25–143.25 |
| final-modern | customer_360 | 0.64–0.7 | 1.2–1.39 | 1.38–1.95 | 5244.45–5962.59 | 2.8–2.8 |
| final-modern | customer_get | 0.08–0.08 | 0.24–0.32 | 0.57–0.7 | 34108.11–36748.07 | 1–1 |
| final-modern | ticket_list | 12.96–13.96 | 35.01–36.97 | 36.68–40.08 | 217.71–229.48 | 1–1 |
