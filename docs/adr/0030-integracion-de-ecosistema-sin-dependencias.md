# 0030 — Integración de ecosistema: sistemas independientes, conectores opcionales

- Estado: Propuesta
- Fecha: 2026-10-17
- Decisores: product owner (conversación del 17/10/2026), arquitectura
- Complementa: [ADR-0018](0018-la-ip-es-el-cliente.md) (la IP sigue siendo el cliente; la identidad
  externa es una anotación opcional, no una dependencia).

## Contexto

El ISP opera más de un sistema: Horus Flow (tráfico, seguridad y salud de la red) y ArgosISP
(gestión del ISP: clientes, servicios PPPoE con RADIUS, facturación, corte y reactivación). Cada
uno sabe algo que el otro necesita:

- Horus ve **qué hace una IP** pero no **de quién es**: hoy el nombre se pone a mano.
- Argos sabe **de quién es la IP** (sesión PPPoE, asignación) pero no **cuánto consume, si su
  servicio está sano ni si está infectada**.

El PO pidió: "que ningún sistema dependa de otro, pero que sí se puedan integrar"; Horus obtiene
el cliente desde Argos y Argos valida el consumo y la salud del servicio desde Horus; además, poder
consultar todo sobre una IP.

## Decisión

### 1. Independencia

- Cada sistema funciona completo sin el otro. La integración es un **conector opcional** que se
  activa pegando un **código de conexión** (token) generado por el otro sistema.
- Si el otro sistema no responde: se conserva el último dato conocido, marcado como desactualizado
  (`stale`), y nada más se degrada. Ningún camino crítico (ingesta, detección, AAA, facturación,
  corte) espera al otro sistema.
- Los contratos son **HTTP públicos y versionados** (OpenAPI de cada sistema) más **webhooks
  firmados**. No se comparten bases de datos, colas ni librerías.

### 2. Códigos de conexión

- **Horus → Argos:** Horus genera un token de integración `hf_pat_…` (diseñado en
  [security.md §5.4](../security.md)) con la plantilla de permisos "ArgosISP" (lectura de clientes,
  consumo, hallazgos y estado de red de un ISP), caducidad y revocación. Se muestra una vez; el
  operador lo pega en Argos.
- **Argos → Horus:** el operador crea en Argos un token `atk_…` con alcance mínimo
  (`ip.resolver`, `clientes.ver`, `servicios.ver`) y lo pega en Horus. Se guarda cifrado y de solo
  escritura, como las demás credenciales de integración.
- Los webhooks de cada sentido se firman con un secreto compartido (HMAC-SHA256) que genera quien
  recibe.

### 3. Identidad del cliente (Argos manda, manual de respaldo)

- La identidad de Horus sigue siendo `(tenant, realm, ip)` (ADR-0018). La identidad externa se
  guarda aparte, **con vigencia** (`valid_from`/`valid_to`), porque en PPPoE la misma IP cambia de
  dueño: un hallazgo de ayer se atribuye al cliente que tenía la IP ayer.
- Con Argos conectado, el alias del cliente toma el nombre de Argos (`alias_source = argosisp`)
  y se muestra su código y estado de servicio. **Un alias puesto a mano nunca lo pisa Argos.**
  Sin Argos, todo sigue como hoy (alias manual).

### 4. Consulta 360 por IP

Horus expone una consulta de "todo sobre una IP" (cliente e identidad externa, consumo, salud del
servicio, seguridad), con la IP en el **cuerpo** de la petición (regla de [api.md](../api.md): las IPs
de clientes nunca van en URLs). La usa la interfaz de Horus y la usa Argos con su código de conexión.

## Consecuencias

- Nuevas piezas en Horus: tokens de integración (`hf_pat_`), consulta 360, conector ArgosISP
  (identidad con vigencia), canal de webhook firmado (alertas) y receptor de webhooks de Argos.
- Nuevas piezas en Argos (su fase 2): conector Horus (consumo, salud y seguridad por servicio),
  emisión de webhooks firmados de cambios de asignación y sesión, y resolución de IP por cuerpo.
- El contrato vive en [`docs/integrations/argosisp.md`](../integrations/argosisp.md) en Horus y su
  espejo en `.claude/integration/contratos/horus.md` en Argos; se cambian con PRs enlazadas en los
  dos repositorios.
- Riesgo: dato personal (nombre del cliente) entra a Horus. Se trata como el alias actual: acceso
  por rol, enmascarado en IA/MCP, no se exporta sin cifrar, se purga con el cliente.
