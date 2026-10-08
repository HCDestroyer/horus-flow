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
