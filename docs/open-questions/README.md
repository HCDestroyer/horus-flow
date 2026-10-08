# Preguntas abiertas — resumen consolidado (ronda 2)

Las respuestas de la ronda 1 están en [`../po-decisions.md`](../po-decisions.md) (D1–D10). Aquí
quedan las preguntas que siguen abiertas, deduplicadas y ordenadas por cuándo bloquean. El detalle
y la recomendación de cada una están en el archivo enlazado.

| Archivo | Área |
| --- | --- |
| [`architecture.md`](architecture.md) | Arquitectura (Q*) |
| [`data.md`](data.md) | Datos, tráfico y almacenamiento (Q*) |
| [`contracts.md`](contracts.md) | API y eventos (C-*) |
| [`security-ops.md`](security-ops.md) | Seguridad, legal, CI e infraestructura (Q*) |
| [`product.md`](product.md) | Producto (P-*) |
| [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §9.2 | MikroTik |

## Bloquean el primer entregable (I1)

| # | Pregunta | Recomendación por defecto | Detalle |
| --- | --- | --- | --- |
| 1 | ¿Horus solo detecta y avisa, o también actúa sobre el router (bloquear, address-lists)? | Solo detectar y avisar en v1; Horus no escribe en el router | P-23, Q23 (arquitectura) |
| 2 | ¿Dónde está el NAT/CGNAT en cada nodo y qué rangos son de clientes? | Flujos exportados del lado del cliente en el router principal | P-24, Q6 (arquitectura), Q3 (datos) |
| 3 | ¿Quién recibe los hallazgos de botnet en cada ISP y qué hace con ellos? | Rol NOC del ISP; aviso en UI y kiosco | P-25 |
| 4 | ¿La UI se expone a Internet? | Detrás de Traefik con TLS; 2FA para administradores | P-26 |
| 5 | Tu MikroTik: modelo, versión de RouterOS, ¿L3HW o FastTrack por hardware? ¿Hay una máquina con KVM para el laboratorio CHR? | RouterOS ≥ 7.12; CHR en KVM | P-06, P-19, mikrotik §9.2 |
| 6 | Confirmar que WireGuard es el túnel saliente del router del nodo hacia Horus | Sí: gestión y envío de flujos | P-07 |

## Antes del segundo incremento (I2)

| # | Pregunta | Recomendación por defecto | Detalle |
| --- | --- | --- | --- |
| 7 | ¿Quién opera Horus: una instalación tuya para varios ISP o una por ISP? | Una instalación compartida multi-ISP | P-27, Q17 (arquitectura) |
| 8 | Escala real: número de ISP, nodos e IPs de clientes | Perfil mínimo hasta ~10k IPs | P-05, Q2 |
| 9 | ¿El cambio a "comercial" es automático o asistido? ¿Qué criterios de uso comercial? | Asistido: el scoring sugiere, el ISP confirma | P-28, Q20 (datos) |
| 10 | Retención por cliente: 25 meses (agregados por nodo 5 años) | Aceptar | Q4 (datos) |
| 11 | 2FA obligatorio, zona horaria, roles por ISP | TOTP para administradores | P-13, P-09, P-03 |

## Antes del tercer incremento (I3)

| # | Pregunta | Recomendación por defecto | Detalle |
| --- | --- | --- | --- |
| 12 | Destino de la copia remota y quién custodia la clave de cifrado | SFTP; paquete de secretos offline en manos de la persona | P-29, Q21 (datos), [ADR-0029](../adr/0029-copias-locales-siempre-y-paquete-de-secretos-offline.md) |
| 13 | ¿Cada ISP tiene su propio destino remoto y su propia retención? | No en v1 | Q20, Q21 (arquitectura) |
| 14 | SMTP, canales de aviso externos y feeds de reputación de pago | Telegram y email; feeds gratuitos primero | P-12, P-14, P-17 |

## Legal (antes de usar tráfico real de terceros)

| # | Pregunta | Detalle |
| --- | --- | --- |
| 15 | País de cada ISP y ley aplicable al tratamiento de tráfico (propósito: seguridad, D5) | P-11, Q14 (arquitectura), security-ops |
| 16 | ¿Se pueden leer datos personales del router (usuario PPPoE, hostname, MAC) y los campos NAT de IPFIX? | mikrotik §9.2, Q22 (datos) |
