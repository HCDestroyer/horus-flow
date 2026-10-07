# Preguntas abiertas — Datos, tráfico y almacenamiento

> Dueño: Agente B (Datos) · Destinatario: Product Owner · Estado: **ronda 2** (tras
> [`po-decisions.md`](../po-decisions.md))
>
> Cada pregunta abierta trae una **recomendación por defecto**: si no hay respuesta antes del
> incremento que la necesita, se avanza con ella. Referencias: [`database.md`](../database.md),
> [`traffic-model.md`](../traffic-model.md), [`storage.md`](../storage.md).

## Abiertas

| # | Pregunta | Por qué importa | Recomendación por defecto | Necesaria antes de |
|---|----------|-----------------|---------------------------|--------------------|
| Q3 | **CGNAT/NAT por ISP**: ¿el NAT lo hace el propio router principal del nodo, otro equipo detrás de él, o un equipo **entre** los clientes y el router principal? (Supuesto abierto del PO, "CGNAT".) | Si el NAT está entre clientes y router principal, la IP observada es la pública compartida y D1 no funciona en ese nodo. Si lo hace el propio MikroTik, se espera ver la IP privada en ambos sentidos (a verificar en laboratorio, [`vendors/mikrotik.md`](../vendors/mikrotik.md) §2.4). | Exportar desde el router que ve la IP del cliente antes del NAT; validar con el primer router real; plan B en [`traffic-model.md` §4.4](../traffic-model.md). Logs de NAT fuera de v1 salvo obligación legal. | Primer incremento con flujos |
| Q4 | **Retención y obligaciones legales por país** de cada ISP (metadatos de conexión, IP como dato personal, auditoría). Con D5 (minimización): ¿se acepta **25 meses** como máximo por defecto para datos **por cliente** (consumo diario, scores, hallazgos) y 5 años solo para agregados por nodo? | Dimensiona disco y define qué se puede guardar por IP. | Crudo 7 d; por cliente 90 d / 13 meses / 25 meses; por nodo hasta 5 años; auditoría 5 años; cada tenant puede acortar. Revisión legal por país antes de producción. | Primer incremento en producción |
| Q7 | **Escala objetivo** a 12 y 24 meses: nº de ISP, nodos por ISP, clientes (IPs) por nodo, tráfico pico. | [`database.md` §8](../database.md) usa escenarios S (9 nodos), M (50 nodos, 50 k IPs) y L (300 nodos, 600 k IPs) con 1 flujo/s por IP. | Dimensionar para **M** con camino documentado a L; medir flujos/s por IP con el primer nodo MikroTik y recalibrar. | Primer incremento con flujos |
| Q8 | ¿Los reportes diarios/mensuales se cortan en **día local** del ISP/nodo o UTC? | Los agregados diarios largos están en día UTC. | Día local desde agregados horarios (13 meses); más allá, día UTC. Zona horaria por tenant con override por nodo. | Incremento de reportes |
| Q11 | ¿Qué se espera de "**dispositivos detrás del CPE**"? | Con NAT en el CPE y solo flujos no se ven dispositivos. | Presentarlo como **estimación con confianza** (diversidad de destinos simultáneos, TTL si el MikroTik lo exporta), nunca como conteo. | Incremento de scoring |
| Q13 | ¿Los ISP usan **IPv6** con clientes y qué tamaño de prefijo delegan (/56, /60, /64)? | Define la longitud que identifica a un cliente IPv6. | Cliente IPv6 = prefijo delegado, **/64 por defecto**, configurable por prefijo de clientes. | Primer incremento con flujos |
| Q14 | ¿Cada ISP tiene **cachés embebidos** (Netflix OCA, Google GGC, Meta FNA, Akamai) o peering en IXP? ¿Con qué subredes? ¿Qué **resolvers DNS** propios? | Sin reglas locales, ese tráfico aparece como "propio/desconocido"; sin la lista de resolvers no se distingue DNS legítimo de DNS a terceros (señal de botnet). | Pedirlo en el alta de cada tenant y cargarlo como overlay del tenant (`local_override`, `dim.tenant_resolver`). | Incremento de clasificación / detección |
| Q15 | ¿Aceptaría algún ISP correlacionar **logs DNS** de sus resolvers (dnstap) con flujos? | Única forma no-DPI de ver nombres consultados: mejora la detección de DGA/C2 (D5) y la clasificación. Implica privacidad. | Fuera de v1; tipo de regla `dns` ya modelado. Decidir tras revisión legal. | Post-v1 |
| Q17 | ¿Hay que **reclasificar el histórico** cuando cambia el catálogo? | Reclasificar agregados largos es caro. | La categoría se reinterpreta siempre; el servicio solo bajo demanda dentro de la retención cruda. | Incremento de clasificación |
| Q20 | **Cambio de tipo por scoring**: ¿se aplica automáticamente (si la confianza ≥ umbral y no está bloqueado) o siempre requiere confirmación del operador? | D1 dice que el tipo "se puede actualizar"; no dice si automática o asistida. | Automático con umbral 80 y histéresis de 7 días, configurable por tenant; el operador puede bloquear manualmente. | Incremento de scoring |
| Q21 | **Custodia de la clave de recuperación** de las copias remotas (`crypt`): ¿quién la guarda (PO, cada ISP, ambos)? | Sin esa clave fuera del servidor, un desastre del servidor hace irrecuperables las copias remotas ([`storage.md` §4.3](../storage.md)). | El dueño de la instalación (plataforma) la guarda fuera del servidor; la UI exige confirmarlo antes de activar un destino. | Primer destino remoto |
| Q22 | **Alias desde PPPoE**: ¿se importa el usuario PPPoE del MikroTik (`/ppp/active`) como alias opcional del cliente? | Ayuda al operador a reconocer IPs, pero es dato personal y no cambia la identidad (D1). | Desactivado por defecto; activable por tenant con retención limitada. | Incremento de clientes |

## Resueltas por el product owner

| # | Pregunta (resumen) | Resolución |
|---|--------------------|------------|
| Q1 | ¿Clientes desde CRM/facturación externo? | **D1**: no hay CRM. La IP es el cliente y se descubre de los flujos ([ADR-0018](../adr/0018-la-ip-es-el-cliente.md)). |
| Q2 | ¿Cómo se asigna IP (PPPoE/RADIUS/DHCP)? | **D1**: irrelevante para la atribución; no se integra RADIUS. Se elimina `client_ip_assignment`. |
| Q5 | ¿Qué routers exportan flujos y cómo? | **D6 + D10**: el router principal MikroTik de cada nodo, Traffic Flow en IPFIX sin muestreo ([`vendors/mikrotik.md`](../vendors/mikrotik.md)). |
| Q6 | ¿Consumo por cliente vía SNMP de interfaces PPPoE? | Consecuencia de **D1/D10**: el consumo por cliente sale de flujos; SNMP no recorre interfaces PPPoE dinámicas. |
| Q9 | Presupuesto de licencias para bases IP→ASN | **D3** ("usar lo recomendado"): fuentes abiertas (RouteViews/RIPE RIS, iptoasn, PeeringDB, rangos publicados); pago solo si la precisión no alcanza. |
| Q10 | ¿ClickHouse desde el primer incremento con series? | **D4**: sí; sin tabla puente en PostgreSQL ([ADR-0021](../adr/0021-clickhouse-desde-el-primer-incremento.md)). |
| Q12 | ¿Qué datos personales del cliente se guardan? | **D1 + D5**: no hay datos de contacto (sin CRM). Solo IP, alias opcional y notas; IP y alias se tratan como datos personales con retención limitada y acceso por rol. |
| Q16 | ¿Qué NAS hay y copia fuera del sitio? | **D2**: NAS opcional; almacenamiento primario local y destino remoto opcional por SFTP/rclone ([ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md)). |
| Q18 | ¿Precisión de facturación? | **D5**: uso exclusivamente de seguridad/operación del ISP; Horus no factura. |
| Q19 | ¿Dashboards personalizados? | **D8**: dashboards modulares por widgets con layout guardado y modo kiosco/NOC. |
