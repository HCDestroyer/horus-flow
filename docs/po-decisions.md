# Decisiones del product owner (ronda 1)

> Respuestas del product owner a las preguntas abiertas del Sprint 0. **Prevalecen sobre
> cualquier documento anterior**, incluido [`vision.md`](vision.md). Cada decisión se registra
> como ADR (0017 en adelante) y los documentos afectados se actualizan para reflejarla.

| # | Decisión del PO | Interpretación para el diseño |
| --- | --- | --- |
| D1 | **La IP es el cliente.** La IP sale de las conexiones que envía el router. Una IP = un cliente. Por defecto es **residencial**; según el tráfico y el comportamiento se puede actualizar su tipo en el sistema, o detectar que una IP residencial tiene uso comercial. | No hay CRM, RADIUS ni facturación como fuente. El cliente se **descubre automáticamente** a partir de las IPs vistas en los flujos del router de cada nodo. Identidad = (ISP, nodo/realm, IP). Tipo por defecto `residential`; cambia por scoring (`commercial`) o manualmente. Se elimina la dependencia crítica Q5/P-04. Se pueden añadir alias/nombres después, pero no son necesarios. |
| D2 | **NAS opcional.** Solo sirve para guardar datos y no es obligatorio tener uno; el sistema debe quedar preparado por si se conecta. Lo práctico es usar **SFTP** para transferir datos a almacenamiento privado (NAS, Google Drive, MEGA, MediaFire, Dropbox). | El almacenamiento primario es **local** al servidor. La copia/archivo externo es un **destino remoto opcional y configurable** (SFTP primero; proveedores de nube después). Ningún componente puede depender de que el NAS exista. Herramienta candidata: rclone (SFTP, Drive, MEGA, Dropbox; MediaFire no tiene soporte estable — validar). |
| D3 | **Licencias: usar lo recomendado.** | **Valkey** en lugar de Redis. MinIO se reevalúa: con D2 deja de ser necesario como capa sobre el NAS; decidir entre almacenamiento local de archivos o un S3-compatible con licencia permisiva (p. ej. Garage, SeaweedFS) solo si hace falta API S3. |
| D4 | **ClickHouse puede existir desde donde se necesite.** | Se adopta desde el primer incremento que guarde series temporales o flujos (SNMP o flows). Se elimina la tabla puente en PostgreSQL. |
| D5 | **Uso exclusivamente empresarial, para mitigar que los clientes entren en botnets.** | El propósito declarado es **seguridad de red del ISP**: detectar clientes infectados o participando en botnets, además del uso comercial. Sube la prioridad de reputación/detección. La privacidad sigue aplicando: minimización, retención limitada y acceso por rol. |
| D6 | **El tráfico viene del router principal de cada nodo de cada ISP.** Puede haber **varios ISP, con varios routers**. | **Multi-tenant desde v1**: tenant = ISP. Jerarquía: ISP → nodo (sitio) → router principal (exportador de flujos) → IPs de clientes. Aislamiento por tenant en datos, API, eventos, permisos y UI. Un usuario puede tener acceso a uno o varios ISP. |
| D7 | **Equipo: IA + 1 persona.** | Desarrollo hecho por agentes de IA con una persona como product owner/revisor. Los procesos (revisiones, CI, Definición de Terminado) deben estar automatizados y ser verificables sin un equipo humano. |
| D8 | **Dashboard modular, para usarlo en pantallas de monitoreo.** | Dashboards compuestos por **widgets** configurables (layout guardado), con **modo kiosco/NOC** para pantallas murales (pantalla completa, rotación, autorrefresco, sin interacción). |
| D9 | **Los "sprints" eran un nombre; los agentes deciden cómo producir un entregable en el menor tiempo posible.** | Se sustituye el calendario fijo de 16 sprints por **incrementos/hitos** ordenados por valor, cada uno desplegable y demostrable. Se busca el primer entregable útil lo antes posible. |
| D10 | **El primer fabricante es MikroTik.** | RouterOS v7: SNMP, Traffic Flow (NetFlow v5/v9/IPFIX), WireGuard nativo y API REST/API de RouterOS. Otros fabricantes después, por adaptadores. |

## Supuestos que quedan abiertos

- ~~WireGuard~~ y ~~NAT/CGNAT~~: confirmados en la ronda 2 (D16, D12).
- **Ley aplicable**: con D5 el tratamiento tiene un propósito de seguridad; aun así se revisará
  por país de cada ISP antes de producción.

# Decisiones del product owner (ronda 2)

> Respuestas a las preguntas que bloqueaban el primer entregable (I1). Prevalecen sobre los
> documentos anteriores; los contratos del Incremento 0 (I0-05) deben reflejarlas.

| # | Decisión del PO | Interpretación para el diseño |
| --- | --- | --- |
| D11 | **Horus solo avisa, pero recomienda qué hacer.** | Horus no escribe en el router (se mantiene ADR-0022). Cada hallazgo incluye **acciones recomendadas** concretas y explicadas (p. ej. comandos RouterOS para aislar la IP en una address-list, limitar puertos, avisar al cliente), listas para que el operador las copie y aplique a mano. |
| D12 | **El NAT está en el router principal del nodo.** | Caso soportado de ADR-0018: los flujos se toman del lado del cliente (pre-NAT) y la IP privada identifica al cliente dentro del realm del nodo. Sigue pendiente verificarlo en el laboratorio CHR (I0-12). |
| D13 | **Alertas por correo o Telegram, y poder usar LibreNMS.** | Canales de notificación email (SMTP) y Telegram. Integración con **LibreNMS** como destino de alertas (por syslog/SNMP trap o su API) y, opcionalmente, como fuente de inventario/estado de los routers. Se adelanta un canal mínimo de alertas al I1. |
| D14 | **La plataforma se accede por Internet, con dominios configurables.** | UI y API públicas detrás de Traefik con TLS automático (Let's Encrypt) y dominio configurable por instalación (posible dominio por ISP más adelante). Exige 2FA para administradores, rate limit y endurecimiento del borde desde el I1. |
| D15 | **Solo RouterOS 7.x.x.** | Se descarta RouterOS v6 por completo. Mínimo 7.12, recomendada la última long-term. |
| D16 | **WireGuard es el túnel que conecta el router con Horus, para que el tráfico se vea de forma transparente.** | Confirma el supuesto: el router principal inicia un túnel WireGuard hacia el hub de Horus; por él viajan gestión (API, SNMP) y exportación de flujos. |

# Decisiones del product owner (ronda 3 — gate G0)

> Respuestas a las preguntas del gate G0 ([`contracts/G0.md`](contracts/G0.md)). Con ellas, los
> contratos v0 quedan **aprobados**; donde el PO no respondió se adopta la recomendación por
> defecto de G0.md. Los contratos se enmiendan para reflejar D17–D21 antes de la Ola 1.

| # | Decisión del PO | Interpretación para el diseño |
| --- | --- | --- |
| D17 | **LibreNMS por su API**, con una instancia dedicada a Horus. La configuración es **manual**: URL de la instancia, usuario, contraseña y token si hace falta. **Por ISP**, para que todo quede aislado aunque varios ISP usen el mismo servidor LibreNMS. | Canal de integración `librenms` configurado por tenant (URL, usuario, contraseña, token opcional; credenciales write-only y cifradas). Horus empuja alertas/hallazgos por la API de LibreNMS. Nada se comparte entre ISPs aunque la URL coincida. Sustituye las variantes syslog/SNMP trap. |
| D18 | **El estado "infectado" se muestra con esa palabra** como indicador. | El estado de seguridad del cliente `infected` se muestra como "Infectado" en UI, kiosco y alertas, siempre con sus razones y nivel de confianza. |
| D19 | **Dominio opcional**: solo si el cliente lo desea; si no, un subdominio, o únicamente la IP del servidor. | Tres modos de acceso por instalación: dominio propio, subdominio, o solo IP. Con dominio o subdominio, TLS automático; con solo IP, certificado autogenerado (o certificado para IP si el emisor lo soporta) y aviso en la consola de plataforma. Nada depende de tener dominio. **Nota de interpretación (I1-22, petición del PO):** se añade un cuarto modo, **TLS externo** (`install.sh --tls external`), sin cambiar los tres anteriores: la persona pone Horus detrás de su propio proxy inverso (Nginx Proxy Manager, nginx, Caddy, HAProxy…), que termina TLS; Traefik sirve solo HTTP a `--trusted-proxies`, sin ACME, sin certificado autogenerado y sin HSTS, y Horus usa la IP real de `X-Forwarded-For` solo desde esos proxies. El acceso sigue siendo por dominio, subdominio o IP (el de `--public-url`); el UDP de WireGuard no pasa por el proxy. Guía: [`install-debian.md`](install-debian.md) §11; controles en [`security.md`](security.md) §3.2. |
| D20 | **Las listas de reputación propuestas se aprueban** (abuse.ch Feodo/ThreatFox, Spamhaus DROP, Tor, RIPE RIS, RIR, PeeringDB). Además, el **superadmin puede agregar las listas que desee** con los permisos adecuados, y se cargan automáticamente a la base de datos. | Las fuentes pasan a `commercial_use: yes`. Nueva capacidad de plataforma: alta de fuentes de reputación personalizadas (URL, formato, frecuencia, categoría, confianza) con permiso de plataforma, auditada; la carga es automática y aparece en el snapshot. CAIDA AS2Org y FireHOL siguen fuera. |
| D21 | **Dueños de los módulos sin asignar: según lo ideal para cada caso.** | `snmp` → FLOW (recolección de telemetría, junto al colector de flujos); `alerts` → CORE (canales de notificación e integraciones, incluido LibreNMS); `jobs` → PLAT (backups, archivado, copia remota). |

Respuestas por defecto de G0 adoptadas: nombres de rol de `security.md`; petición sin ISP → 403
`TOKEN_SCOPE_INVALID`; sin replay de WebSocket en v1 (resincronización por REST); un usuario
puede tener varios roles en un ISP; token de enrolamiento en el cuerpo JSON (a verificar en CHR);
email y Telegram sin IPs de clientes por defecto; bot de Telegram configurable por ISP (mismo
criterio de aislamiento que D17).

# Decisiones del product owner (ronda 4 — IPv6)

| # | Decisión del PO | Interpretación para el diseño |
| --- | --- | --- |
| D22 | **Un abonado puede tener o no IPv6; cada versión se toma como un cliente más**, validado según la versión que use. **Agrupar IPv6 por prefijo está bien.** | IPv4 e IPv6 son clientes independientes: la IP IPv4 es un cliente y el prefijo delegado IPv6 (truncado a `ipv6_client_len`, /64 por defecto) es otro. No se vinculan entre sí en v1 (cierra E-IPv6-4). Tipo, scoring y hallazgos se calculan por cliente, por separado para cada versión. |

# Decisiones del product owner (ronda 5 — resiliencia y escala)

| # | Decisión del PO | Interpretación para el diseño |
| --- | --- | --- |
| D23 | **Tras cualquier reinicio todo debe funcionar al 100 %**, con **logs** que dejen rastro para investigar cualquier fallo. El sistema debe funcionar en un **escenario crítico con hardware pequeño** y también en un **escenario ideal**, con **escalamiento horizontal y sub-servidores**. El trabajo **multihilo** es bienvenido **siempre que funcione al 100 %**. | (1) Ningún estado vive solo en memoria: todo consumidor con estado se reconstruye al arrancar, y cada rol tiene prueba de reinicio brusco (`kill -9`) sin pérdida, sin duplicados y con el mismo resultado; las pérdidas inevitables (UDP con el colector caído) se acotan con spool a disco y quedan medidas y visibles. (2) Logs estructurados y persistentes con rotación, correlación (`trace_id`, ISP, router) y un registro de eventos de plataforma (arranques, caídas, migraciones, degradaciones), más un paquete de diagnóstico exportable sin datos de clientes. (3) Perfiles de despliegue desde un servidor pequeño (perfil crítico, degradación ordenada y medida) hasta clúster: colectores remotos como sub-servidores con búfer local, réplicas sin estado de `horus-app`, NATS en clúster/leafnodes y ClickHouse/PostgreSQL replicados. (4) Toda paralelización entra solo con pruebas de equivalencia (mismo resultado que en un hilo), `-race` y caos en verde. |
