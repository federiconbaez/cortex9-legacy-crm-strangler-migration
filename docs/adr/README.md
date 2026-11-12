# Decisiones de arquitectura (ADR)

Formato: contexto, decisión, alternativas, consecuencias y cómo se verifica.

| ADR | Decisión | Estado |
|---|---|---|
| [0001](0001-fachada-strangler-por-ruta.md) | Migrar con una fachada strangler y flags por ruta, no con una reescritura | Aceptada |
| [0002](0002-caracterizacion-antes-de-migrar.md) | Fijar el comportamiento con tests de caracterización y golden master antes de tocar nada | Aceptada |
| [0003](0003-conservar-el-contrato-heredado.md) | Conservar las rarezas del contrato JSON durante la migración | Aceptada |
| [0004](0004-telefono-e164-con-expand-contract.md) | Teléfono normalizado E.164 con expand/contract y cuarentena | Aceptada |
| [0005](0005-benchmark-en-proceso-con-escenarios-aislados.md) | Benchmark en proceso con escenarios que aíslan cada efecto | Aceptada |
