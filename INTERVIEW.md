# Preguntas de entrevista: migración strangler de un CRM legado

Preguntas difíciles sobre este repo, con una respuesta modelo y los trade-offs. Los números salen de `results/` y se regeneran con `scripts/bench-repeticiones.sh` y `scripts/migracion-medida.sh`.

---

### 1. ¿Por qué strangler y no una reescritura?

Porque el legado tiene comportamiento no documentado del que dependen clientes, y una reescritura junta todo el riesgo en un solo corte después de meses sin entregar valor. Con la fachada, cada ruta se migra, se mide y se revierte con un flag. **Trade-off:** durante la transición hay dos sistemas, una base compartida y un componente más (la fachada) para operar.

### 2. ¿Qué es un test de caracterización y en qué se diferencia de un test normal?

Un test normal verifica lo que el código **debería** hacer. Uno de caracterización fija lo que **hace hoy**, incluidos los bugs. Es la red de seguridad para reemplazar código sin especificación. Aquí: 24 requests grabadas del legado (golden master) que el nuevo, la fachada y ambos durante la migración tienen que reproducir.

### 3. ¿No es raro conservar bugs como "no encontrado = 200"?

Es deliberado. Si cambiás el motor y el contrato a la vez y algo se rompe, no sabés cuál de los dos fue. Además la sombra mostraría diferencias en todas las requests y perdería valor como detector. Las rarezas están aisladas en `compat.go`, marcadas con `QUIRK`, y se retiran con una `/v2` anunciada (ADR-0003).

### 4. ¿Cómo funciona la sombra y por qué no frena al usuario?

El legado responde; la fachada encola una copia y un worker la ejecuta contra el nuevo, compara status y JSON campo por campo y registra métricas. La cola es acotada (256) con 2 workers: si se llena, se descarta y se cuenta. **Trade-off:** bajo carga alta se compara una muestra; a cambio, la sombra nunca agrega latencia al camino del usuario.

### 5. ¿Por qué no se puede poner una escritura en sombra?

Porque ejecutarla dos veces duplica efectos (dos actualizaciones, dos emails, dos cobros). La fachada lo rechaza al configurar el flag. Para validar escrituras se usa otra técnica: escribir en el nuevo y comparar el estado resultante, o dual write con verificación como en la migración del teléfono.

### 6. ¿Cómo comparás dos JSON? ¿Qué pasa con campos que cambian a propósito?

Comparación semántica: el orden de claves no importa, el orden de arrays sí y `null` es distinto de `[]`. Cada diferencia lleva la ruta del campo. Las diferencias intencionales se declaran como reglas por ruta y por fase, con el motivo. Solo el teléfono después de `reads_switched` está aceptado. En las métricas los índices de arrays se normalizan (`tickets[].agent`) para no explotar la cardinalidad.

### 7. Explicá el canary. ¿Por qué hashear y no elegir al azar?

`FNV-1a(ruta + clave) % 100 < porcentaje`. Con azar, el mismo cliente alternaría entre sistemas en cada request y vería respuestas inconsistentes. Con hash es **pegajoso**, y además **monótono**: subir del 10 % al 50 % no saca a nadie. La ruta va en el hash para que el 10 % de una ruta no sean siempre los mismos clientes en todas las rutas.

### 8. ¿Qué pasa si el nuevo falla durante el canary?

En lecturas, si el nuevo da error o 5xx, la fachada reintenta en el legado y lo cuenta en `facade_fallback_total`. El cliente recibe la respuesta del legado. En escrituras no hay fallback automático, porque el reintento podría duplicar el efecto si el nuevo llegó a escribir.

### 9. ¿Qué es expand/contract?

Cambiar el esquema sin downtime en fases: expandir (agregar lo nuevo sin quitar nada), migrar con ambos conviviendo y contraer (borrar lo viejo) al final. Aquí: `none → expanded → dual_write → backfilled → verified → reads_switched → contracted`, con cada fase persistida y con precondiciones.

### 10. ¿Por qué dual write antes del backfill y no después?

Si el backfill corre primero, todo lo que se escribe mientras corre queda solo en la columna vieja: drift. Con dual write activo antes, cada escritura nueva ya actualiza las dos columnas y el backfill solo tiene que ponerse al día con lo histórico.

### 11. ¿Cómo hacés el backfill de una tabla grande sin trabarla?

Por lotes con keyset (`WHERE id > ? ORDER BY id LIMIT ?`), una transacción corta por lote, idempotente (solo escribe lo que difiere) y reanudable. En producción le sumaría pausas entre lotes según el lag de replicación. El keyset evita el `OFFSET`, que se vuelve más lento a medida que avanza.

### 12. ¿Qué hacés con los teléfonos que no se pueden normalizar?

Van a una tabla de cuarentena con el texto original. No se inventa un valor ni se pierde el dato, y queda una lista para que alguien los corrija. El nuevo los muestra tal cual estaban. La verificación exige que cada inválido esté en cuarentena y que cada válido esté normalizado.

### 13. ¿Qué es el drift y cómo lo detectás?

Filas donde la columna nueva no coincide con lo que daría normalizar la vieja. Aparece si algo escribe sin dual write, como el legado llamado directo. `Verify` compara fila por fila y bloquea el cambio de lecturas y el contract si el drift no es 0. Está probado: una escritura del legado genera drift en exactamente ese cliente y un nuevo backfill lo repara.

### 14. ¿Por qué el contract exige que ninguna ruta llegue al legado?

Porque el legado traga errores: después del `DROP COLUMN` respondería 200 "not found" en silencio, sin alertas. El test de integración lo demuestra llamando al legado directo después del contract. Es el único paso sin vuelta atrás; por eso tiene la precondición más dura.

### 15. ¿Cuál fue el resultado más sorprendente del benchmark?

Que el código nuevo **sin índices** es mucho más lento que el legado en la vista 360. El plan de SQLite muestra que su JOIN recorre las 30 000 interacciones y busca el ticket de cada una, más un ordenamiento temporal, mientras el legado hace recorridos simples por ticket. Arreglar el N+1 sin índices puede empeorar las cosas. Lo vi porque el benchmark separa "solo índices" de "solo código".

### 16. ¿Por qué el listado nuevo no mejora el p95 frente al legado con índices?

Porque conserva el orden heredado, que ordena por una expresión (`strftime('%d/%m/%Y …')` como texto) y no puede usar un índice: ordena todos los tickets filtrados en cada request. Pasa de unas 142 consultas a 1, y el p50 mejora, pero el p95 queda por encima del legado con índices. La solución es cambiar el contrato a un orden cronológico real, con un índice compuesto `(status, created_at)`.

### 17. ¿Cuánto cuesta la fachada?

Con todo en legacy, la latencia de las rutas pesadas queda en el mismo orden que el legado directo (comparar `fachada-legacy` con `baseline-legacy` en `results/repeticiones.md`). En proceso cuesta poco; en producción sería un salto de red más, que hay que medir allá.

### 18. ¿Por qué SQLite y no MySQL para el benchmark?

Reproducibilidad: un comando, sin Docker, igual en CI. El costo es que los números absolutos no se trasladan a MySQL por red. Lo que sí se traslada: las consultas por request (no dependen de la máquina) y la dirección de los efectos (índices, N+1). Con más tiempo repetiría las mediciones clave en MySQL.

### 19. ¿Cómo sabés que tus números no son ruido?

Tres repeticiones con rango mín–máx, mismo dataset y mismas requests, calentamiento descartado y una sola cosa corriendo en la máquina. Las conclusiones que reporto son las que se sostienen en las tres corridas; las p99 en una máquina compartida tienen saltos aislados y no las uso para concluir.

### 20. ¿Qué métricas mirarías durante la migración en producción?

Por ruta: `shadow_mismatch_total` (debe ser 0), `shadow_dropped_total` (si crece, se está comparando una muestra), `facade_fallback_total` (el nuevo falla), latencia por backend y la fase de la migración (`migration_phase`). Del lado de la base: lag de replicación durante el backfill y drift de `Verify`.

### 21. El legado usa una variable global para la base. ¿Qué problema trae?

Solo puede haber un entorno activo por proceso, lo que complica los tests en paralelo y obliga a cuidar el orden de apertura y cierre. No lo arreglé dentro del legado a propósito: se estrangula desde afuera. El nuevo recibe sus dependencias por constructor.

### 22. ¿Cómo revertís si el nuevo tiene un bug después de pasar a `modern`?

Cambiando el flag de la ruta a `legacy`, sin deploy. Funciona hasta el contract, porque el legado todavía puede leer la columna vieja (que se mantuvo actualizada por dual write). Después del contract, revertir exige restaurar la columna, por eso es el último paso y con precondiciones.

### 23. ¿Qué le falta a este diseño para producción?

Autenticación en `/admin`, flags en un almacén compartido (no en memoria de cada instancia), backfill con throttling según el lag, verificación por rangos con checksums en réplica, trazas distribuidas a través de la fachada y alertas sobre las métricas de sombra.

### 24. Si tuvieras que migrar a una base distinta (no compartida), ¿qué cambia?

La sombra vería diferencias por replicación (el nuevo lee datos que todavía no llegaron). Haría falta CDC (por ejemplo, Debezium sobre el binlog) para sincronizar, una ventana de tolerancia en la comparación y un plan para el corte de escrituras: dual write con outbox o un período corto de solo lectura.

### 25. ¿Cómo se relaciona esto con un equipo de CRM de atención al cliente?

Los sistemas de atención acumulan años de reglas no escritas y no se pueden apagar: los agentes los usan todo el día. Este enfoque permite modernizar la vista 360 y el listado de tickets (lo que más usan los agentes y lo que más consultas hacía) sin cortar el servicio y con evidencia medible en cada paso.
