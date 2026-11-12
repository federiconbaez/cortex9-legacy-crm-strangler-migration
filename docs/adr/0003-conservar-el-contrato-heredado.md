# ADR-0003: conservar el contrato heredado durante la migración

## Contexto

El legado responde 200 a "no encontrado", devuelve colecciones `null`, muestra prioridades fuera de catálogo como `""` y ordena el listado comparando fechas `dd/mm/aaaa` como texto. Hay clientes que pueden depender de cualquiera de esas cosas.

## Decisión

El nuevo reproduce cada rareza, aislada en `internal/modern/compat.go` y marcada con `QUIRK`. El dominio interno usa tipos correctos (`time.Time`, enteros); la presentación heredada es una capa aparte. Cada rareza se retira después, con una versión nueva de la API (`/v2`) y aviso a los consumidores, no a escondidas.

## Alternativas

- **Corregir todo en la migración:** dos cambios a la vez; si algo se rompe, no se sabe si fue el motor o el contrato. Además la sombra mostraría diferencias en todas las requests y dejaría de servir para detectar errores reales.
- **Corregir solo lo "obvio":** cualquier criterio de "obvio" es una apuesta sobre consumidores desconocidos.

## Consecuencias

- (+) La sombra muestra 0 mismatches sobre todo el tráfico del benchmark: cualquier diferencia futura es una señal real.
- (−) Medido: el orden heredado del listado no puede usar un índice, porque ordena por una expresión (`strftime('%d/%m/%Y …')`). El listado final hace 1 consulta en lugar de unas 142, pero su p95 queda por encima del legado con índices (ver `results/repeticiones.md`). Corregirlo exige el cambio de contrato.
- (−) Código de compatibilidad que hay que mantener hasta `/v2`.

## Verificación

`characterization/quirks_test.go` corre cada rareza contra el legado y contra el nuevo.
