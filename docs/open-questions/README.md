# Preguntas abiertas — resumen consolidado

Cada agente del Sprint 0 dejó sus preguntas con recomendación en este directorio. Aquí están
agrupadas y deduplicadas, ordenadas por cuándo bloquean el trabajo. El detalle y la
recomendación completa están en el archivo enlazado.

| Archivo | Área |
| --- | --- |
| [`architecture.md`](architecture.md) | Arquitectura (Q1–Q16) |
| [`data.md`](data.md) | Datos, tráfico y almacenamiento |
| [`contracts.md`](contracts.md) | API y eventos (C-01–C-17) |
| [`security-ops.md`](security-ops.md) | Seguridad, legal, CI e infraestructura |
| [`product.md`](product.md) | Producto y equipo (P-01–P-22) |

## Bloquean el Sprint 1

| # | Pregunta | Recomendación por defecto | Detalle |
| --- | --- | --- | --- |
| 1 | ¿Qué hardware/servidores hay y qué modelo de NAS? ¿Soporta S3 nativo o iSCSI? | MinIO en el NAS o sobre un LUN iSCSI; nunca sobre NFS | Q4, Q16 (arquitectura), security-ops, data |
| 2 | ¿Aceptamos las licencias de MinIO y Redis? | Valkey en lugar de Redis; reevaluar MinIO frente a otro S3-compatible | Q11 (arquitectura) |
| 3 | Tamaño y composición del equipo humano | 4 flujos paralelos (ver [`../backlog/team.md`](../backlog/team.md)) | P-02 |
| 4 | Plan de GitHub (Actions, CodeQL) | GitHub Actions | P-15, security-ops |
| 5 | ¿Multi-tenant? | Un solo ISP en v1 con `tenant_id` en las tablas raíz | Q1 |

## Bloquean el Sprint 3–4

| # | Pregunta | Recomendación por defecto | Detalle |
| --- | --- | --- | --- |
| 6 | **¿De dónde sale el mapa IP ↔ cliente?** (RADIUS, CRM, facturación, API del router) | Adaptador como módulo de `devices`; investigación en S4 | Q5, P-04 — **crítica**: sin ella S7, S9 y S10 no dan valor por cliente |
| 7 | ¿Hay CGNAT? ¿Dónde se exportan los flujos? | Exportar en el borde que ve la IP del cliente antes del NAT | Q6, data |
| 8 | ¿Para qué es WireGuard? ¿Horus configura los routers? | Hub central de gestión; Horus genera claves y config | Q3, Q9, P-07 |
| 9 | Adelantar ICMP al Sprint 3 para el dashboard online/offline | Sí | Q7 |
| 10 | ¿SMTP disponible? | Restablecimiento asistido por admin en S2 hasta tenerlo | P-12 |

## Bloquean el Sprint 5–7

| # | Pregunta | Recomendación por defecto | Detalle |
| --- | --- | --- | --- |
| 11 | ClickHouse en el MVP (Sprint 5, para series SNMP) en lugar del Sprint 6 | Aceptar (cambia `vision.md` §14; requiere ADR-0017) | P-18, data |
| 12 | Escala real y crecimiento a 2–3 años; fabricantes y protocolos de flujo | — | Q2, P-05, P-06 |
| 13 | Retención de flujos raw y muestreo | Raw 7 días; muestreo desde ~200 routers | Q10 |
| 14 | Avisos de router caído antes del Sprint 11 | Notificaciones UI desde S5 + Alertmanager para routers core | Q13, P-17 |
| 15 | Licencias de bases prefijo→ASN→organización y feeds de reputación | — | data, P-14 |

## Legales (antes de guardar tráfico real)

| # | Pregunta | Detalle |
| --- | --- | --- |
| 16 | País del ISP y ley de privacidad aplicable al tráfico de suscriptores | Q14, P-11, security-ops |
| 17 | Retención legal de auditoría y de asignaciones IP | security-ops, data |
| 18 | RTO/RPO exigidos y destino de la copia offsite | Q12, security-ops |
