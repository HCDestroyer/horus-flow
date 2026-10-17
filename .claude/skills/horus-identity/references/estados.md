# Estados: color + icono + texto

Regla: la **urgencia** decide el color, el **icono** distingue la identidad y el **texto** siempre
está. Crítica y Alta comparten color porque comparten urgencia; se distinguen por icono y texto.
Los estados de flujo de trabajo (abierto, resuelto…) son `neutral`: la urgencia la da la severidad.

| Familia | Valor | Color | Icono (Lucide) | Texto |
| --- | --- | --- | --- | --- |
| Severidad | Crítica | `error` | `i-lucide-octagon-alert` | Crítica |
| | Alta | `error` | `i-lucide-triangle-alert` | Alta |
| | Media | `warning` | `i-lucide-circle-alert` | Media |
| | Baja | `neutral` | `i-lucide-info` | Baja |
| Estado de hallazgo / ticket | Abierto · Reconocido · Resuelto · Falso positivo | `neutral` | iconos propios | texto |
| Exportador / fuente de datos | Exportando | `success` | `i-lucide-radio-tower` | Exportando |
| | Pendiente de configurar | `neutral` | `i-lucide-circle-dashed` | Pendiente |
| | Silencioso | `error` | `i-lucide-radio-off` | Silencioso · desde 10:42 |
| | Con pérdidas / reloj desfasado | `warning` | `i-lucide-triangle-alert` | Con pérdidas · 3 % |
| Equipo (router, servidor, servicio) | online | `success` | `i-lucide-circle-check` | En línea |
| | degraded | `warning` | `i-lucide-circle-alert` | Degradado · razón |
| | offline | `error` | `i-lucide-circle-x` | Fuera de línea · desde… |
| | stale / unknown | `neutral` | `i-lucide-circle-help` | Sin datos recientes |
| | maintenance | `info` | `i-lucide-wrench` | Mantenimiento |
| Tipo de cliente | Residencial / Comercial | `neutral` / `info` | `i-lucide-house` / `i-lucide-building-2` (+ `i-lucide-lock` si es manual) | texto + origen |
| Actividad | Activo / Inactivo | `neutral` (Inactivo atenuado) | — | texto |
| Frescura | En vivo | `primary` | punto `.live-dot` | En vivo · hace 5 s |

Componentes: en Nuxt UI, `UBadge` con `color` semántico y `variant="subtle"`; los tonos de
estado en claro van un paso más oscuros para pasar 4,5:1 también sobre su fondo tintado (10 %).

Para un concepto nuevo: ¿requiere acción ya? → `error`; ¿conviene mirarlo? → `warning`;
¿todo bien? → `success`; ¿solo informa o clasifica? → `info`/`neutral`. Nunca `primary` para un
estado (es la marca y la acción).
