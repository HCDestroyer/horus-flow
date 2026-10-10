# Política de seguridad

Horus Flow es software de seguridad de red para ISPs; nos tomamos en serio cualquier
vulnerabilidad.

## Cómo reportar una vulnerabilidad

Escribe a **info@kns.gt** con el asunto `[SEGURIDAD] Horus Flow`. **No abras un issue público**
ni publiques detalles hasta que esté corregida.

Incluye, si puedes:

- versión afectada (`horus-ctl version`) y modo de despliegue;
- descripción del fallo y su impacto (p. ej. acceso entre ISPs, escalada de privilegios,
  exposición de datos de clientes);
- pasos para reproducirlo o prueba de concepto;
- si lo has visto explotado.

No envíes datos reales de clientes ni capturas de tráfico sin anonimizar; si un paquete de
`horus-ctl diagnose` ayuda, ya enmascara IPs, correos y tokens.

## Qué puedes esperar

| Paso | Plazo objetivo |
| --- | --- |
| Acuse de recibo | 3 días hábiles |
| Primera evaluación (gravedad y alcance) | 10 días hábiles |
| Corrección de vulnerabilidades críticas o altas | lo antes posible, en una versión de parche |

Te mantendremos informado del avance y, si lo deseas, te reconoceremos en las notas de la versión
que lo corrige.

## Versiones con soporte

| Versión | Soporte de seguridad |
| --- | --- |
| 1.x (última menor) | Sí |
| Anteriores a 1.0 | No |

Las correcciones de seguridad se publican como parches (`X.Y.Z`) y se pueden aplicar con
`horus-ctl upgrade` o, si está activada, con la actualización automática de parches.

## Alcance

Entran en el alcance el binario `horus`, la interfaz web, el instalador y `horus-ctl`, las imágenes
publicadas y los scripts de alta de routers. Las vulnerabilidades de componentes de terceros
(ClickHouse, PostgreSQL, NATS, Valkey, Traefik, RouterOS) repórtalas a su proyecto; si afectan a
Horus Flow, avísanos también.
