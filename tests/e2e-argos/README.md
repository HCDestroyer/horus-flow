# tests/e2e-argos — Horus Flow ↔ ArgosISP de extremo a extremo

Prueba repetible del contrato [`docs/integrations/argosisp.md`](../../docs/integrations/argosisp.md) con los **dos
sistemas corriendo de verdad**: el binario `horus` (nativo, sin imagen Docker) y la API, el relay del outbox,
`horus:worker`, `sesiones:worker` y el servicio `aaa` de ArgosISP desde su checkout. Solo PostgreSQL, ClickHouse,
NATS y Valkey van en contenedores de imágenes públicas. Nada se simula salvo lo que vendría de la red: los flujos (se
escriben en los agregados de ClickHouse), los eventos de detección y SNMP (se publican en NATS como lo haría su outbox)
y el accounting RADIUS (se manda a `aaa` como `rlm_rest`).

## Uso

```bash
make e2e-argos ARGOS_DIR=/ruta/a/argos-isp       # up + prueba + down; ~4 min tras el primer `pnpm install`
make e2e-argos ARGOS_DIR=… E2E_STEP=up           # solo levantar (bases nuevas en cada `up`)
make e2e-argos ARGOS_DIR=… E2E_STEP=test         # correr la prueba contra lo levantado
make e2e-argos ARGOS_DIR=… E2E_STEP=down         # parar procesos y borrar contenedores
E2E_RUN='TestE2E/S0[0-5]' make e2e-argos …       # solo algunos escenarios (go test -run)
```

Requisitos: Docker, Go, Node 22 y pnpm 10 (`ARGOS_DIR` necesita `pnpm install`; `up` lo hace si falta
`node_modules`), `openssl` y `curl`. Por defecto `ARGOS_DIR` es `../argos-isp` junto al repositorio. Puertos (todos
en 127.0.0.1, cambiables con `E2E_*_PORT`): PostgreSQL 55432, ClickHouse 58123/59000, NATS 54222, Valkey 56379, Horus
18080 (admin 18081), ArgosISP 13333, aaa 13100. Logs, `summary.txt` y `test.log` quedan en `.e2e-argos/` (ignorado
por Git); los secretos de laboratorio (KEK, clave de firma) se generan ahí en cada `up`.

## Escenarios

| Id | Qué prueba (sección del contrato) |
| --- | --- |
| S00 | Arranque; siembra: dos ISP en Horus (B con un cliente de la misma IP que A), `pnpm demo:datos` en Argos |
| S01 | §1.4 con Horus = A: enlace nuevo anula el anterior, vencido → 410, un solo uso (el canje fallido no deja nada), conectores y canal webhook apagados, auditoría en los dos lados |
| S02 | §1.4 con Argos = A: la misma petición repetida → misma respuesta, otra → 410, anulado, vencido, 422 sin consumir el código; el Horus real canjea; un canje nuevo revoca el `atk_` anterior |
| S03 | Nada fluye con los conectores apagados (refresh 409, receptor 404, autologin 409/422); se activan; pruebas de conexión en los dos sentidos |
| S04 | §1.1/§1.2: `hf_pat_` lee §3 (incl. `routers/status` e `infra-services`) y no administra; `atk_` solo resuelve; revocar corta el acceso |
| S05 | §2.1/§2.2: resolver por lotes en el cable (campos, orden, `no_encontrada`, máx. 500), respaldo `GET /ip/:ip` real tras un proxy que da 404 al resolver, identidad con empresa, alias, cliente descubierto por el bus y barrido |
| S06 | §2.3: `cliente.actualizado`, `servicio.estado_cambiado`, `asignacion.cambiada` (cierra la vigencia), `sesion.iniciada/terminada` (accounting real), `servicio.config_cambiada` (empresa); firma, desfase, conector desconocido, deduplicación y rotación del secreto |
| S07 | §3.1/§3.2/§3.5: 360 por IP, por servicio y en el pasado; consumo por servicio sumando cada IP solo en su vigencia; seguridad por lotes; lo mismo desde las pantallas de Argos |
| S08 | §3.4: `ping`, `finding.opened`, `finding.resolved` (resuelto por la API de Horus), `customer.security_state_changed`, `router.status_changed`, `infra_service.state_changed` → eventos internos `horus.*` de Argos (sin cambiar el estado del servicio); firma, desfase y deduplicación del receptor de Argos |
| S09 | §1.5 Horus → Argos: vinculación con login posterior al canje, sin vincular por correo, ticket de un uso, ACL de Argos (alcance por router, sin permisos de escritura), solo el `atk_` del canje |
| S10 | §1.5 Argos → Horus: `link_required`, vinculación con login posterior, sesión con el ACL de Horus (sin `security.findings.read` → 403) |
| S11 | §0 degradación: Argos caído → identidad `stale` conservada en Horus; Horus caído → ficha de Argos "desactualizada" y el webhook se reintenta a 1 min hasta llegar |
| S12 | Datos personales solo con `include_personal_data`; secretos nunca en claro en bitácoras ni vistas; un `hf_pat_` del ISP B no lee nada de A; un `atk_` con alcance de router no resuelve otros |

Al final imprime una tabla `Sxx PASS/FAIL` y la deja en `.e2e-argos/summary.txt`.
