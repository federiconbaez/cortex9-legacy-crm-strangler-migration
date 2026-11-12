# ADR-0001: fachada strangler con flags por ruta

## Contexto

El monolito CRM tiene deuda grave (N+1, estado global, errores tragados) y clientes que dependen de su API tal como está. No tiene tests. No se puede apagar.

## Decisión

Poner una fachada HTTP delante que enrute **por ruta** al legado o al nuevo según un flag con cuatro modos (`legacy`, `shadow`, `canary` con porcentaje estable por clave, `modern`). Lo que la fachada no conoce va al legado. Solo las rutas sin efectos pueden ir en sombra o tener fallback. Los flags cambian en caliente.

## Alternativas

- **Reescritura completa (big bang):** meses sin entregar valor y todo el riesgo en un solo corte.
- **Refactor dentro del monolito:** posible, pero sin tests y con estado global cada cambio es riesgoso, y no permite comparar viejo contra nuevo con tráfico real.
- **Enrutar en el balanceador o el API gateway:** sirve para modos simples por ruta, pero la sombra con comparación y el canary por cliente necesitan lógica propia.

## Consecuencias

- (+) Cada ruta avanza y retrocede sola, sin deploy.
- (+) Medido: con todo en legacy, la fachada no cambia el orden de magnitud de las latencias de las rutas pesadas (ver `fachada-legacy` contra `baseline-legacy` en `results/repeticiones.md`).
- (−) Un salto de red más en producción y un componente más para operar.
- (−) Mientras conviven, la base se comparte y obliga a migrar el esquema con expand/contract (ADR-0004).

## Verificación

`internal/facade/facade_test.go`, `flags_test.go` (asignación pegajosa y monótona) e `integration/rollout_test.go` (recorrido completo con servidores HTTP reales).
