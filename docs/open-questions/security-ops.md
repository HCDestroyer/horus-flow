# Preguntas abiertas — Seguridad, observabilidad y plataforma

> Ronda 2 · Responsable: Agente C. Documentos: [`security.md`](../security.md), [`observability.md`](../observability.md),
> [`disaster-recovery.md`](../disaster-recovery.md), [`conventions.md`](../conventions.md). Decisiones del PO en
> [`po-decisions.md`](../po-decisions.md). Si no hay respuesta antes del incremento indicado, se aplica la
> recomendación y se registra como ADR.

## Resueltas por el product owner

| # | Pregunta | Resolución |
|---|----------|------------|
| Q3 | ¿El scoring residencial/comercial puede tener consecuencias contractuales? | **Reencuadrada por D1 + D5**: el cliente es la IP y el propósito es la seguridad de la red y el uso comercial. El tipo es explicable, con historial y bloqueo manual; cualquier acción sobre el cliente la decide una persona del ISP ([`security.md`](../security.md) §13.3). Queda abierta sólo la parte legal por país (Q1). |
| Q4b | Plazo para anonimizar PII de suscriptores dados de baja | **Obsoleta por D1**: Horus no guarda nombres, direcciones ni contratos; sólo IPs (y alias opcionales). La purga de clientes-IP sigue la retención por cliente (25 meses sin actividad). |
| Q9 | Modelo de NAS, ¿S3 o contenedores? | **Resuelta por D2/D3** ([ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md)): NAS opcional, sólo como destino remoto por SFTP; sin MinIO. |
| Q10 | Licencias de MinIO y Redis | **Resuelta por D3**: Valkey; sin MinIO ([ADR-0020](../adr/0020-valkey-en-lugar-de-redis.md)). |
| Q11 | Destino offsite de backups | **Resuelta por D2**: destino remoto **opcional y configurable** (SFTP primero; Google Drive, Dropbox, MEGA después; MediaFire no), cifrado en cliente; sin destino el sistema avisa y no falla ([`disaster-recovery.md`](../disaster-recovery.md) §3.0). Qué proveedor usar lo elige quien opere cada instalación. |
| Q19 | Pantallas NOC 24/7 frente a la expiración de sesión | **Resuelta por D8**: modo kiosco con dispositivo registrado (código de un uso → credencial HttpOnly rotativa → token de solo lectura del tenant, CIDR, sin datos personales por defecto) ([`security.md`](../security.md) §5.5, [`api.md`](../api.md) §2.12). |
| Q5 (parcial) | ¿GitHub Actions? | D7 exige CI automatizado como única garantía de calidad; se asume **GitHub Actions** (el repo ya está en GitHub). Queda abierto el plan (Q6). |

## Abiertas

### Legal y privacidad

| # | Pregunta | Por qué importa | Recomendación | Antes de |
|---|----------|-----------------|---------------|----------|
| Q1 | ¿En qué **países** operan los ISP y qué ley aplica a cada uno? ¿Quién es responsable y quién encargado (contrato de encargo por ISP)? | D5 da un propósito de seguridad, pero la base legal, la DPIA y los avisos dependen del país de cada tenant | Campo `country` por tenant; DPIA por país antes de ingerir flujos reales; Horus como encargado por cuenta de cada ISP | Incremento 3 (flujos) |
| Q2 | ¿Retención **mínima o máxima** legal de metadatos de tráfico por país? | Puede contradecir la propuesta (crudo 7 días, por cliente 25 meses) | Mantener la propuesta, configurable por tenant dentro de límites de plataforma | Incremento 3 |
| Q4 | Retención legal de la **auditoría** | Coste y cumplimiento | 2 años en PostgreSQL + 5 años archivada | Incremento 1 |
| Q24 | ¿Puede la plataforma (la persona que opera Horus) **acceder a datos de un ISP** para soporte? ¿Con aviso o con aprobación del ISP? | Aislamiento y confianza entre ISPs | Acceso de soporte temporal (≤ 4 h), con motivo, notificado al `tenant_admin` y auditado; cada ISP puede exigir aprobación o denegarlo ([`security.md`](../security.md) §6.6) | Incremento 1 |
| Q25 | ¿Quién opera la plataforma respecto a los ISP: el propio ISP principal, un tercero, un SaaS? | Define responsable/encargado, DPA y quién ve qué | Tratar al operador como encargado de cada ISP en todos los casos | Incremento 1 |
| Q26 | Baja de un ISP: ¿plazo de conservación antes de purgar y qué se le entrega? | Contrato y privacidad | Exportación cifrada + purga a 30 días; las copias caducan en 35 días ([`security.md`](../security.md) §13.5) | Antes del 2º ISP |

### Plataforma, CI y repositorio

| # | Pregunta | Por qué importa | Recomendación | Antes de |
|---|----------|-----------------|---------------|----------|
| Q6 | ¿Repo privado? ¿Plan de GitHub (merge queue, rulesets, GHAS)? | La merge queue y los checks requeridos son la base del flujo con agentes (D7); CodeQL/secret scanning privados requieren plan de pago | Plan que incluya merge queue en repos privados; si no hay GHAS, semgrep + gitleaks | Incremento 1 |
| Q7 | Registro de imágenes; ¿el servidor tiene salida a Internet? | Despliegue y actualizaciones; el enrolamiento de routers necesita que el servidor sea alcanzable por HTTPS | GHCR; servidor con salida a Internet y nombre DNS público para el hub y el enrolamiento | Incremento 1 |
| Q27 | ¿Identidad de bot (GitHub App) para los agentes y cuenta personal para la persona? | Distinguir la aprobación humana de la de agentes en las reglas de protección ([`conventions.md`](../conventions.md) §6.2) | Sí | Incremento 1 |

### Hardware e infraestructura

| # | Pregunta | Por qué importa | Recomendación | Antes de |
|---|----------|-----------------|---------------|----------|
| Q8 | ¿Qué servidor hay (CPU, RAM, **número de discos**)? ¿Hay repuesto? | RTO real y si la copia local protege frente a la pérdida de un disco | 1 servidor con **dos discos o volúmenes físicos** (datos / almacén y copias); RAID1 si es posible | Incremento 1 |
| Q12 | ¿Se aceptan los RPO/RTO realistas para un servidor operado por IA + 1 persona? | Sustituyen a los del Sprint 0 | PG RPO ≤ 5 min local / ≤ 1 h remoto (SFTP); RTO ≤ 4 h laborables con disco de repuesto, 1–2 días laborables si se pierde el servidor; sin destino remoto, pérdida total posible ([`disaster-recovery.md`](../disaster-recovery.md) §2) | Incremento 1 |
| Q28 | ¿Quién custodia el **paquete de secretos offline** (clave `crypt`, KEK, llave age)? | Sin él no hay recuperación aunque haya copias | La persona, en gestor de contraseñas + USB cifrado fuera del servidor | Incremento 1 |

### Red y acceso

| # | Pregunta | Por qué importa | Recomendación | Antes de |
|---|----------|-----------------|---------------|----------|
| Q13 | ¿UI expuesta a Internet o sólo VPN? Con varios ISP, cada uno accede desde su red | Superficie de ataque | Expuesta con TLS, 2FA obligatorio para todos los roles con datos personales, rate limit; el endpoint de enrolamiento sí debe ser público | Incremento 1 |
| Q14 | Dominio y certificados | El hub WG y el enrolamiento usan un nombre DNS (cambiarlo es el plan de DR del hub) | Dominio público con ACME; registro DNS del hub con TTL bajo | Incremento 1 |
| Q15 | Confirmar que **todo** router llega por WireGuard (gestión y flujos) | Seguridad de SNMP/API y unicidad del exportador por tenant | Sí; ningún router exporta por Internet en claro (ver Q22 de [`architecture.md`](architecture.md)) | Incremento 2 |
| Q16 | ¿Versiones de RouterOS en campo? ¿SNMPv3 con SHA-256? | Credenciales y adaptador | RouterOS ≥ 7.12; SNMPv3 authPriv | Incremento 2 |
| Q17 | ¿Algún ISP exige SSO con su IdP? | S1 | Auth propio; federación por tenant si se pide | Incremento 1 |

### Políticas de seguridad del producto

| # | Pregunta | Por qué importa | Recomendación | Antes de |
|---|----------|-----------------|---------------|----------|
| Q18 | ¿2FA obligatorio para todos? | Varios ISP y UI en Internet | Obligatorio para todo rol con escritura o con `customers.read`/`traffic.customer.read`/`security.evidence.read` y para todo rol de plataforma | Incremento 1 |
| Q20 | ¿Re-descargar configuración WireGuard de un router? | Custodia de claves | Resuelto en la práctica por D10 (la clave la genera el router); se re-genera el script `.rsc` sin clave privada | Incremento 2 |
| Q21 | Canal de alertas de la plataforma para la **única** persona | Una sola persona de guardia en horario laboral | Telegram + email; `page` sólo para fallos multi-tenant, de plataforma o de seguridad; heartbeat externo ([`observability.md`](../observability.md) §6.2) | Incremento 1 |
| Q22 | ¿Pentest externo antes de 1.0? | Validación independiente, más necesaria sin equipo humano de revisión | Sí, centrado en aislamiento entre tenants, kiosco y enrolamiento | Antes de 1.0 |
| Q23 | Idiomas de la UI | i18n desde el inicio | Español por defecto, claves desde el primer incremento | Incremento 1 |
