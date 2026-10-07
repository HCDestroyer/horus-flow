# Preguntas abiertas — Seguridad, observabilidad y plataforma

> Responsable: Agente 4 · Sprint 0. Destinatario: product owner.
> Cada pregunta incluye la recomendación por defecto: si no hay respuesta antes del Sprint
> indicado, se aplica la recomendación y se registra como ADR.
> Documentos: [`security.md`](../security.md), [`observability.md`](../observability.md),
> [`disaster-recovery.md`](../disaster-recovery.md), [`conventions.md`](../conventions.md).

## Legal y privacidad

| # | Pregunta | Por qué importa | Recomendación por defecto | Necesaria antes de |
|---|----------|-----------------|---------------------------|--------------------|
| Q1 | ¿En qué país opera el ISP y qué ley de protección de datos aplica? ¿Hay DPO/responsable de privacidad? | Los flujos de abonados son datos personales y metadatos de comunicaciones; condiciona retención, acceso, perfilado (Sprint 10) y notificación de brechas | Diseñar con el estándar más estricto razonable (principios tipo RGPD): minimización, retención limitada, acceso auditado, EIPD/DPIA antes de producción | Sprint 6 (ingesta de flujos) |
| Q2 | ¿Hay obligación legal de **conservar** metadatos de tráfico (mínimo) o de **no** conservarlos más allá de X (máximo)? ¿Quién puede requerirlos (autoridades) y con qué procedimiento? | Puede contradecir la retención de vision Sprint 13 (crudo 7–30 días) | Mantener la retención de vision §13 hasta validar; no usar Horus como sistema de retención legal sin revisión | Sprint 13 |
| Q3 | ¿El scoring residencial/comercial (Sprint 10) puede tener consecuencias contractuales para el cliente? ¿Hay que informar en el contrato/aviso de privacidad? | Perfilado automatizado con efectos sobre personas | Resultado solo como sugerencia explicable; decisión humana; validar texto del aviso de privacidad | Sprint 10 |
| Q4 | ¿Retención legal de la **auditoría** y de las **asignaciones IP→cliente** (`subscriber_ip_assignment`)? ¿Puede activarse Object Lock *compliance* (irreversible)? | Coste y cumplimiento; las asignaciones permiten re-identificar flujos ([`database.md`](../database.md)) | Auditoría: 2 años en PostgreSQL + 5 años en `horus-audit`; asignaciones IP: 13 meses y borrado salvo obligación legal; governance hasta validar plazos, luego compliance | Sprint 2 (auditoría) / Sprint 6 (asignaciones) |
| Q4b | ¿Plazo para anonimizar la PII de suscriptores dados de baja? | Política de [`security.md`](../security.md) §13.3 | 90 días tras `terminated` | Sprint 6 |

## Plataforma, CI y repositorio

| # | Pregunta | Por qué importa | Recomendación por defecto | Necesaria antes de |
|---|----------|-----------------|---------------------------|--------------------|
| Q5 | ¿GitHub Actions o GitLab CI? | Vision §10 permite ambos | **GitHub Actions**: el repo ya está en GitHub (`hcdestroyer/horus-flow`), OIDC nativo para firmar con cosign, GHCR, attestations, Renovate | Sprint 1 |
| Q6 | ¿El repo será privado? ¿Qué plan de GitHub (Free/Team/Enterprise, GHAS)? | CodeQL, secret scanning con push protection y rulesets avanzados en repos privados requieren GHAS/plan de pago; minutos de Actions limitados | Si no hay GHAS: semgrep + gitleaks en CI (gratuitos); si los minutos no alcanzan: runner autoalojado efímero | Sprint 1 |
| Q7 | ¿Registro de imágenes: GHCR o uno interno (Harbor)? ¿Los servidores de producción tienen salida a Internet? | Despliegue, firmas, actualizaciones | GHCR; si producción está aislada, espejo Harbor interno con verificación de firmas | Sprint 1 |

## Hardware e infraestructura

| # | Pregunta | Por qué importa | Recomendación por defecto | Necesaria antes de |
|---|----------|-----------------|---------------------------|--------------------|
| Q8 | ¿Qué servidores hay disponibles (CPU, RAM, discos SSD/NVMe, nº de máquinas, virtualización)? ¿Hay hardware de reemplazo? | Dimensionamiento de ClickHouse/PostgreSQL, RTO real, HA del Sprint 14 | Mínimo v1: 1 servidor 16 vCPU / 64 GB RAM / 2 TB NVMe (RAID1) para la app + NAS; segundo nodo para HA y staging en Sprint 14 | Sprint 1 |
| Q9 | ¿Modelo de NAS? ¿Soporta S3 nativo o contenedores (para correr MinIO sobre discos locales)? ¿Capacidad y RAID? | MinIO **no** debe usar NFS/SMB como backend; el NAS es destino de archivo y backups | Si el NAS ejecuta contenedores → MinIO en el NAS sobre volúmenes locales; si no → MinIO en el servidor con LUN iSCSI del NAS. Validar con el Agente 2 ([`storage.md`](../storage.md)) | Sprint 1 |
| Q10 | Licencias: **MinIO** comunitario (AGPL; imágenes y consola restringidas desde 2025) y **Redis** ≥ 7.4 (RSAL/SSPL/AGPL). ¿Se acepta, o se usan alternativas? | Riesgo de mantenimiento y licencia | Valkey en lugar de Redis (coincide con el Agente 1); para S3 mantener la API como contrato y evaluar Garage/SeaweedFS/Ceph RGW en Sprint 1 con el Agente 2 (ver también [`open-questions/architecture.md`](architecture.md)) | Sprint 1 |
| Q11 | ¿Destino **offsite** para backups (nube S3, segundo sitio del ISP)? ¿Presupuesto mensual? | Regla 3-2-1; sin offsite, un incendio/ransomware en el sitio pierde todo | S3 en la nube con Object Lock (p. ej. Backblaze B2 / Wasabi / AWS S3 Glacier IR) cifrado del lado cliente | Sprint 13 (PostgreSQL desde Sprint 2) |
| Q12 | ¿Valores de RPO/RTO aceptables para el negocio? | Los de [`disaster-recovery.md`](../disaster-recovery.md) §2 son propuesta | PG RPO 5 min / RTO 1 h; plataforma completa RTO 4 h | Sprint 2 |

## Red y acceso

| # | Pregunta | Por qué importa | Recomendación por defecto | Necesaria antes de |
|---|----------|-----------------|---------------------------|--------------------|
| Q13 | ¿La UI se expondrá a Internet o solo a la red corporativa/VPN del ISP? | Superficie de ataque | **Solo red interna/VPN** en v1; si se expone, WAF + 2FA obligatorio para todos | Sprint 1 |
| Q14 | ¿Dominio y certificados: dominio público con ACME o CA interna del ISP? | TLS del Sprint 1 | Subdominio público con ACME DNS-01 aunque el servicio sea interno | Sprint 1 |
| Q15 | ¿Existe VLAN/VRF de gestión para los routers? ¿Los routers remotos llegan por WireGuard? ¿Por dónde exportarán flujos? | Segmentación y validación de origen de colectores | Flujos y SNMP por VLAN de gestión o por el túnel WG del router; nunca por Internet en claro | Sprint 4–6 |
| Q16 | ¿Cuántos routers y de qué fabricantes/modelos? ¿Soportan SNMPv3 authPriv (SHA-256/AES)? | Credenciales, capacidad, adaptadores | Objetivo SNMPv3 authPriv; v2c solo dentro de túnel | Sprint 5 |
| Q17 | ¿Existe un IdP corporativo (Entra ID, Google Workspace, Keycloak) con el que se quiera SSO? | Decisión S1 de [`security.md`](../security.md) | Auth propio en v1; federación OIDC como RP cuando se pida | Sprint 2 |

## Políticas de seguridad del producto

| # | Pregunta | Por qué importa | Recomendación por defecto | Necesaria antes de |
|---|----------|-----------------|---------------------------|--------------------|
| Q18 | ¿2FA obligatorio para **todos** los usuarios o solo roles privilegiados? | UX vs seguridad | Obligatorio para todos los que tengan cualquier permiso de escritura o `traffic.client.read`; recomendado para el resto | Sprint 2 |
| Q19 | ¿Pantallas NOC de pared que deben quedar logueadas 24/7? | Choca con la expiración de sesión de 12 h | Rol `viewer` + "dispositivo kiosco" con token de solo lectura revocable, limitado a IP y a dashboards concretos | Sprint 9 |
| Q20 | ¿Se deben poder **re-descargar** configuraciones WireGuard de peers (implica guardar la clave privada del peer cifrada)? | Riesgo de custodia de claves | No guardar privadas de peers; regenerar par si se pierde la config (ya es la decisión por defecto de [`database.md`](../database.md)) | Sprint 4 |
| Q21 | ¿Canal y responsables de guardia para las alertas de la plataforma (email, Telegram, teléfono)? ¿Horario? | Alertmanager independiente del servicio `alerts` | Email + Telegram para `page`; email para `ticket`; heartbeat externo | Sprint 1 |
| Q22 | ¿Se contratará pentest externo antes de la Release 1.0? | Validación independiente | Sí, en Sprint 15–16 sobre staging | Sprint 14 |
| Q23 | ¿Idiomas de la UI (solo español o también inglés)? | i18n desde el inicio es más barato | `@nuxtjs/i18n` con español por defecto, claves desde Sprint 1 | Sprint 1 |
