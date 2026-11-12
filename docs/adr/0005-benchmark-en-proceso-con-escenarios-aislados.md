# ADR-0005: benchmark en proceso con escenarios que aíslan cada efecto

## Contexto

Hace falta mostrar "antes y después" con números reales y reproducibles, en una máquina compartida y en CI, sin Docker ni una base externa.

## Decisión

- SQLite en Go puro (`modernc.org/sqlite`, sin cgo), modo WAL, en archivo local.
- Generador de datos con semilla fija y requests con semilla fija.
- Seis escenarios, cada uno sobre una copia de la misma base: línea base, solo índices, solo código, fachada en legacy, fachada en sombra y estado final. Así se separa el efecto de los índices del efecto del código y se mide el costo de la fachada y de la sombra.
- Consultas por request contadas en serie con un contador en el driver: es la métrica que no depende de la máquina.
- 3 repeticiones con rango mín–máx. En CI corre una versión `-quick` como humo y un test fija las consultas por request, sin comparar tiempos.

## Alternativas

- **MySQL en contenedor:** más parecido a producción, pero no hay Docker en el entorno y el CI tardaría más.
- **Una sola comparación antes/después:** mezcla efectos y lleva a conclusiones falsas. Este diseño mostró que el código nuevo sin índices es **más lento** en la vista 360.
- **Carga de lazo abierto:** mejor para latencia de cola bajo picos; acá el objetivo era comparar implementaciones con el mismo trabajo.

## Consecuencias

- (+) Reproducible con un comando y sin dependencias externas.
- (−) Los números absolutos no se trasladan a MySQL por red; las proporciones y las consultas por request sí orientan.
- (−) Lazo cerrado con 4 clientes: no mide el comportamiento ante picos.

## Verificación

`internal/bench/bench_test.go` (humo de toda la matriz con consultas por request y 0 mismatches), `scripts/bench-repeticiones.sh` y `results/`.
