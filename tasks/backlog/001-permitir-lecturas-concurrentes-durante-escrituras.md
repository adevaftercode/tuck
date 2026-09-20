---
id: tuck_01M307F28X8AK1Q0K3PSB2T5KD
number: 1
order: 1
created_at: "2026-09-20T20:17:34.49307509Z"
---
# Permitir lecturas concurrentes durante escrituras

Cambiar la coordinación para que el lock exclusivo serialice solo a los
escritores. Cada comando que modifica el tablero debe tomarlo antes de cargarlo
y mantenerlo hasta terminar el commit; así se protegen las operaciones de
lectura-modificación-escritura y la asignación de números.

`list` y `show` deben poder leer mientras una escritura está en curso. Para
construir una vista coherente, consultar el manifiesto publicado en
`tasks/.tuck/txn` y aplicar virtualmente sus cambios usando las copias staged:
ocultar rutas marcadas para borrar y sustituir las demás por su contenido
posterior. No buscar temporales por nombre ni ejecutar recuperación desde una
lectura. Si una transacción empieza o termina mientras se construye la vista,
detectar el cambio y reintentar para obtener el estado anterior o posterior
completo.

La consistencia prometida es una lectura completa en un instante; que quede
obsoleta después forma parte del flujo de trabajo de los agentes. Resolver
aparte cómo `check` distingue una transacción activa de una interrumpida.

## Criterios de aceptación

- `list` y `show` no esperan al lock de escritura y no observan el intervalo
  físico entre borrar y crear rutas durante un movimiento.
- Las lecturas usan una vista anterior o posterior completa del manifiesto, sin
  contenido de archivo parcialmente escrito ni errores transitorios de
  validación por una operación en curso.
- Los escritores siguen serializados desde antes de cargar el tablero hasta
  después del commit; dos `add` concurrentes reciben números distintos.
- Las lecturas no ejecutan `Recover`; la recuperación que modifica archivos se
  hace bajo el lock de escritura.
