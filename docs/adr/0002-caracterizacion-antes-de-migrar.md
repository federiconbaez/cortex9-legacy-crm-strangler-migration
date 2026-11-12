# ADR-0002: caracterización y golden master antes de migrar

## Contexto

No hay especificación ni tests del legado. "Igual que antes" no se puede verificar si no se sabe qué hacía antes.

## Decisión

Grabar la respuesta exacta del legado (status y cuerpo) para un guion de 24 requests que incluye lecturas, escrituras y errores, y exigir el mismo resultado al nuevo, a la fachada en cada modo y a ambos en cada fase de la migración. Además, nombrar las rarezas en tests propios para que nadie las "arregle" sin querer.

## Alternativas

- **Escribir tests de lo que "debería" hacer:** fallarían contra el legado; no sirven como red de seguridad de un reemplazo.
- **Solo comparar en sombra en producción:** detecta diferencias tarde y con datos reales; la caracterización las detecta en CI, antes del deploy.
- **Contratos con Pact o similar:** útiles cuando hay consumidores identificados; acá el contrato real es "lo que hace el legado".

## Consecuencias

- (+) El nuevo se pudo escribir con libertad interna (otro modelo, otras consultas) sabiendo que el contrato no cambió.
- (−) El golden congela también los bugs (ADR-0003).
- (−) Un cambio intencional de contrato exige regenerar el golden con `-update` y revisar el diff en el PR.

## Verificación

`characterization/` (golden y rarezas), corre en cada CI.
