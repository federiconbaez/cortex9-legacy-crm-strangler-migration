# ADR-0004: teléfono E.164 con expand/contract y cuarentena

## Contexto

`customers.phone` es texto libre con formatos mezclados. El nuevo necesita el teléfono normalizado (E.164) para integrarse con canales como SMS o WhatsApp. La base es compartida con el legado y no puede haber downtime.

## Decisión

Migración expand/contract en fases persistidas (`none → expanded → dual_write → backfilled → verified → reads_switched → contracted`), donde cada fase exige la anterior:

- Columna nueva nullable, índices y tabla de cuarentena en el expand.
- Dual write **antes** del backfill.
- Backfill por lotes con keyset, idempotente y reanudable.
- Lo que no se puede normalizar con confianza va a cuarentena con el texto original.
- Verify con drift 0 antes de cambiar lecturas y antes de contraer.
- Contract solo sin rutas que lleguen al legado.

Después de `reads_switched` el teléfono sale normalizado (`+54 11 4321-0000`); es la **única** diferencia aceptada respecto del contrato heredado, declarada por ruta en `app.AcceptedDiffs` con este ADR como motivo.

## Alternativas

- **Normalizar en la lectura sin migrar datos:** cada lectura repite el trabajo y los inválidos quedan invisibles.
- **Renombrar o cambiar el tipo de la columna en un paso:** rompe al legado en el acto.
- **Descartar los teléfonos inválidos:** pérdida de datos silenciosa.

## Consecuencias

- (+) Reversible hasta el contract; el contract está protegido por precondiciones.
- (+) Probado: sin pérdida de datos (`e164 + cuarentena = clientes`), drift detectado si un escritor no hace dual write y reparado con otro backfill.
- (−) El normalizador cubre los formatos de este dataset, no es libphonenumber.
- (−) El legado llamado directo después del contract responde 200 "not found" (traga el error). Por eso el contract exige `LegacyReaders() == 0`.

## Verificación

`internal/migration/migration_test.go`, `characterization/golden_test.go` (`TestGoldenAfterReadSwitch` y `TestGoldenAfterContract`) e `integration/rollout_test.go`.
