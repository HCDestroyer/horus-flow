# Sprint 2 — Usuarios, roles y seguridad

- **Objetivo del sprint:** identidad y autorización de grado producción: los administradores
  gestionan usuarios, roles y sesiones; cada usuario puede activar TOTP; cada acción sensible
  queda auditada; nadie ve ni invoca lo que su rol no permite.
- **Ajustes respecto a `vision.md`** (justificados en [`../roadmap.md`](../roadmap.md) §4.1):
  la **aplicación** de ACL a sitios/grupos pasa a S3 (S03-08); la recuperación de contraseña por
  email pasa a S3/S11 según P-12; en S2 se entrega el restablecimiento asistido por administrador.
  El **backup de PostgreSQL** se adelanta a este sprint (§4.7).
- **Épicas:** EP-03, EP-04, EP-01, EP-T2.
- **Flujos:** Plataforma (P), Backend core (B), Frontend (F), Datos (D).

| ID | Historia | Flujo | Área | Pts | Prio | Depende de |
| --- | --- | --- | --- | --- | --- | --- |
| S02-01 | Catálogo de permisos y roles predefinidos | B | backend, security | 3 | Must | S01-10 |
| S02-02 | Enforcement de permisos en gateway y servicios | B | backend, security | 5 | Must | S02-01 |
| S02-03 | API de usuarios | B | backend | 5 | Must | S02-01 |
| S02-04 | API de roles personalizados | B | backend | 3 | Should | S02-01 |
| S02-05 | Sesiones: listado y revocación | B | backend, security | 3 | Must | S01-09 |
| S02-06 | TOTP: enrolamiento, verificación y códigos de respaldo | B | backend, security | 5 | Must | S01-08 |
| S02-07 | Restablecimiento de contraseña asistido por admin | B | backend, security | 3 | Must | S02-03 |
| S02-08 | Registro de auditoría | B, D | backend, data | 5 | Must | S02-02 |
| S02-09 | Motor ACL genérico | B | backend, security | 5 | Should | S02-02 |
| S02-10 | Pantalla: usuarios | F | frontend | 5 | Must | S02-03 |
| S02-11 | Pantalla: roles y permisos | F | frontend | 3 | Must | S02-01, S02-04 |
| S02-12 | Pantalla: sesiones (mías y de todos) | F | frontend | 3 | Must | S02-05 |
| S02-13 | Pantalla: auditoría | F | frontend | 3 | Must | S02-08 |
| S02-14 | Flujo de login con TOTP y Mi cuenta › Seguridad | F | frontend | 5 | Must | S02-06 |
| S02-15 | `usePermission` y reglas de UI por permiso | F | frontend | 2 | Must | S01-09 |
| S02-16 | Backup diario de PostgreSQL con prueba de restauración | P | infra | 3 | Must | S01-01 |
| S02-17 | Pruebas de matriz de permisos | P, B | security | 3 | Must | S02-02 |
| S02-18 | Esquema y consumidor base de ClickHouse (preparación) | D | data | 3 | Could | — |

Total: 64 pts (B ~33, F 21, P ~5, D ~5).

---

### S02-01 · Catálogo de permisos y roles predefinidos
- **Épica:** EP-04 · **Pts:** 3 · **Servicio:** auth

**Como** administrador de plataforma **quiero** roles predefinidos coherentes **para** dar acceso
sin diseñar permisos uno a uno.

1. **Dado** la base de `vision.md` §7 y [`../security.md`](../security.md), **cuando** se aplica la
   migración, **entonces** existen los permisos del catálogo de [`../security.md`](../security.md)
   §6.1 (`users.*`, `roles.read/manage/assign`, `sessions.*`, `audit.read/export`, `sites.*`,
   `devices.*`, `wireguard.*`, …) y los roles de sistema de §6.2: `admin`, `security_admin`,
   `network_engineer`, `noc_operator`, `analyst`, `auditor`, `viewer` (etiquetas en español en la
   UI; validar con P-03 si falta un rol de *técnico de campo*).
2. **Dado** un rol predefinido, **cuando** se intenta borrar, **entonces** responde 409.
3. **Dado** el catálogo, **cuando** el frontend pide `GET /api/v1/permissions`, **entonces** recibe
   cada permiso con descripción legible y agrupado por recurso.

### S02-02 · Enforcement de permisos
- **Épica:** EP-04 · **Pts:** 5 · **Servicio:** api-gateway, auth, devices

1. **Dado** cada ruta, **cuando** se declara en el gateway, **entonces** tiene permiso requerido
   explícito; una ruta sin permiso declarado no arranca (fallo en el test de inicio).
2. **Dado** un usuario sin el permiso, **cuando** llama, **entonces** recibe 403 con
   `code: permission_denied` y el permiso faltante en `details`.
3. **Dado** que se revoca un rol a un usuario, **cuando** hace su siguiente petición, **entonces** el
   cambio se aplica en ≤ duración del access token (o inmediatamente si `security.md` define
   invalidación por evento `horus.auth.user.roles_changed`).
4. **Dado** los servicios internos, **cuando** reciben una petición del gateway, **entonces**
   verifican de nuevo el permiso (defensa en profundidad), no confían solo en el gateway.

### S02-03 · API de usuarios
- **Épica:** EP-03 · **Pts:** 5 · **Servicio:** auth

**Como** administrador **quiero** crear, editar, desactivar y asignar roles a usuarios **para**
controlar quién usa la plataforma.

1. **Dado** `users.manage`, **cuando** creo un usuario con username, nombre, email y roles,
   **entonces** se crea con contraseña temporal de un solo uso o enlace de activación, y debe
   cambiarla en el primer login.
2. **Dado** un username o email duplicado, **cuando** creo, **entonces** 409 con el campo en conflicto.
3. **Dado** un usuario, **cuando** lo desactivo, **entonces** se revocan todas sus sesiones y no puede
   iniciar sesión; los usuarios no se borran físicamente (se conserva auditoría).
4. **Dado** que soy el último administrador activo, **cuando** intento desactivarme o quitarme el
   rol, **entonces** responde 409 "Debe quedar al menos un administrador".
5. **Dado** `GET /api/v1/users`, **cuando** filtro por estado, rol y texto, **entonces** obtengo
   resultados paginados y ordenables.

### S02-04 · Roles personalizados
- **Épica:** EP-04 · **Pts:** 3 · **Prio:** Should

1. **Dado** `roles.manage`, **cuando** creo un rol con un conjunto de permisos, **entonces** queda
   disponible para asignación.
2. **Dado** un rol asignado a usuarios, **cuando** lo borro, **entonces** 409 con el número de
   usuarios afectados.

### S02-05 · Sesiones
- **Épica:** EP-03 · **Pts:** 3 · **Servicio:** auth

**Como** usuario **quiero** ver dónde tengo sesión abierta y cerrarla **para** reaccionar si pierdo un
dispositivo.

1. **Dado** que estoy autenticado, **cuando** pido mis sesiones, **entonces** veo creación, último
   uso, IP, agente de usuario resumido y cuál es la actual.
2. **Dado** una sesión, **cuando** la revoco, **entonces** su refresh token deja de funcionar y su
   WebSocket se cierra en < 5 s.
3. **Dado** `sessions.manage`, **cuando** listo sesiones de todos, **entonces** puedo revocarlas;
   sin el permiso, 403.
4. **Dado** "Cerrar las demás sesiones", **cuando** lo ejecuto, **entonces** se revocan todas menos la
   actual.

### S02-06 · TOTP
- **Épica:** EP-03 · **Pts:** 5 · **Servicio:** auth

**Como** usuario **quiero** proteger mi cuenta con un segundo factor **para** que una contraseña
filtrada no baste.

1. **Dado** que activo 2FA, **cuando** inicio el enrolamiento, **entonces** recibo secreto y URI
   `otpauth://` (QR) y no queda activo hasta verificar un código válido.
2. **Dado** el enrolamiento verificado, **cuando** termina, **entonces** recibo 10 códigos de respaldo
   de un solo uso que se muestran **una sola vez** y se guardan hasheados.
3. **Dado** 2FA activo, **cuando** paso usuario y contraseña, **entonces** recibo un desafío
   intermedio (sin tokens de sesión) que requiere TOTP o código de respaldo.
4. **Dado** un código reutilizado en la misma ventana de tiempo, **cuando** se envía, **entonces** se
   rechaza (antireplay); 5 fallos activan el mismo limitador que el login.
5. **Dado** la política (P-13), **cuando** un rol tiene 2FA obligatorio, **entonces** el usuario
   debe enrolarse antes de acceder a cualquier otra pantalla.
6. **Dado** el secreto TOTP, **cuando** se almacena, **entonces** está cifrado según
   [`../security.md`](../security.md).

### S02-07 · Restablecimiento asistido por administrador
- **Épica:** EP-03 · **Pts:** 3

**Como** administrador **quiero** restablecer la contraseña o el 2FA de un usuario **para**
desbloquearlo sin acceso a base de datos.

1. **Dado** `users.manage`, **cuando** restablezco la contraseña, **entonces** se genera un enlace de un
   solo uso con caducidad (p. ej. 24 h) que copio y entrego por un canal externo; se revocan sus
   sesiones.
2. **Dado** el restablecimiento de 2FA, **cuando** lo ejecuto, **entonces** el usuario debe
   re-enrolarse en el siguiente login.
3. **Dado** cualquiera de las dos acciones, **cuando** ocurre, **entonces** queda en auditoría con
   actor, objetivo y motivo obligatorio.

### S02-08 · Registro de auditoría
- **Épica:** EP-04 · **Pts:** 5 · **Servicio:** auth (+ consumidor común)

**Como** analista de seguridad **quiero** un registro de quién hizo qué y cuándo **para** investigar
incidentes.

1. **Dado** login (éxito y fallo), logout, cambios de usuario/rol/permisos, revocación de sesión,
   2FA y restablecimientos, **cuando** ocurren, **entonces** se registra un evento de auditoría con
   `id` (UUIDv7), `occurred_at` (UTC), actor, acción, recurso, resultado, IP, `trace_id` y diff de
   campos cambiados (sin secretos).
2. **Dado** los servicios, **cuando** auditan, **entonces** publican el evento de auditoría
   definido en [`../events.md`](../events.md) vía outbox
   ([ADR-0016](../adr/0016-transactional-outbox.md)) y el consumidor de `auth` persiste en
   `audit_log` (encadenado por hash, [`../database.md`](../database.md)); si NATS no está
   disponible, el evento no se pierde.
3. **Dado** `audit.read`, **cuando** consulto `GET /api/v1/audit` con filtros por actor,
   acción, recurso y rango de fechas, **entonces** obtengo resultados paginados en < 500 ms p95 con
   1 M de filas.
4. **Dado** la tabla de auditoría, **cuando** un usuario de aplicación intenta UPDATE/DELETE,
   **entonces** la base de datos lo impide (permisos de rol de BD).

### S02-09 · Motor ACL genérico
- **Épica:** EP-04 · **Pts:** 5 · **Prio:** Should

**Como** administrador **quiero** poder limitar permisos a un subconjunto de recursos **para** que un
técnico solo opere los routers de su zona (se aplica en S3).

1. **Dado** el modelo ACL de [`../security.md`](../security.md), **cuando** se crea una concesión
   `sujeto (usuario|rol) – tipo de recurso – id o grupo – acción`, **entonces** auth la evalúa junto
   al RBAC.
2. **Dado** un usuario con `devices.read` sin restricción ACL, **cuando** se evalúa, **entonces** el
   acceso es global (ACL restringe, no concede más que el RBAC).
3. **Dado** la evaluación, **cuando** se mide, **entonces** añade < 2 ms p95 por petición (caché en
   Redis invalidada por evento).

### S02-10 · Pantalla de usuarios
- **Épica:** EP-04 · **Área:** frontend · **Pts:** 5

1. **Dado** `users.read`, **cuando** abro Administración › Usuarios, **entonces** veo una `UTable`
   con nombre, usuario, roles (`UBadge`), estado, 2FA (icono + texto) y último acceso; con búsqueda,
   filtros por rol y estado, orden y paginación sincronizados con la URL.
2. **Dado** `users.manage`, **cuando** pulso "Nuevo usuario", **entonces** se abre un `USlideover`
   con `UForm` validado en línea; al guardar se cierra, aparece un toast "Usuario creado" y la fila
   nueva.
3. **Dado** que no tengo `users.manage`, **cuando** abro la pantalla, **entonces** no veo los botones
   de crear/editar/desactivar (regla "ocultar por permiso", [`../frontend.md`](../frontend.md) §9).
4. **Dado** "Desactivar", **cuando** lo elijo, **entonces** un `UModal` de confirmación explica que se
   cerrarán sus sesiones; el botón destructivo dice "Desactivar usuario", no "Aceptar".
5. **Dado** que no hay más usuarios que yo, **cuando** abro la pantalla, **entonces** el estado vacío
   invita a "Invitar al primer compañero".

### S02-11 · Pantalla de roles y permisos
- **Épica:** EP-04 · **Área:** frontend · **Pts:** 3

1. **Dado** `roles.manage`, **cuando** edito un rol, **entonces** veo los permisos agrupados por
   recurso con `UCheckbox` y descripción legible, y un resumen "12 permisos · 4 usuarios".
2. **Dado** un rol predefinido, **cuando** lo abro, **entonces** es de solo lectura con la razón
   visible.

### S02-12 · Pantalla de sesiones
- **Épica:** EP-03 · **Área:** frontend · **Pts:** 3

1. **Dado** Mi cuenta › Sesiones, **cuando** la abro, **entonces** veo mis sesiones con la actual
   marcada y "Cerrar sesión" por fila y "Cerrar las demás".
2. **Dado** `sessions.manage`, **cuando** abro Administración › Sesiones, **entonces** veo las de
   todos, filtrables por usuario.
3. **Dado** que otra persona revoca mi sesión actual, **cuando** ocurre, **entonces** la app me lleva a
   `/login` con el mensaje "Tu sesión fue cerrada por un administrador".

### S02-13 · Pantalla de auditoría
- **Épica:** EP-04 · **Área:** frontend · **Pts:** 3

1. **Dado** `audit.read`, **cuando** abro Administración › Auditoría, **entonces** veo una tabla
   cronológica con fecha (zona horaria del usuario, UTC en tooltip), actor, acción, recurso y
   resultado; filtros por rango (`UInputDate`/`UCalendar`), actor y acción.
2. **Dado** una fila, **cuando** la abro, **entonces** un `USlideover` muestra el diff antes/después y
   el `trace_id` copiable.
3. **Dado** el enlace a la auditoría filtrada, **cuando** lo comparto, **entonces** reproduce los
   mismos filtros (estado en la URL).

### S02-14 · Login con TOTP y Mi cuenta › Seguridad
- **Épica:** EP-03 · **Área:** frontend · **Pts:** 5

1. **Dado** 2FA activo, **cuando** paso usuario y contraseña, **entonces** veo un paso con `UPinInput`
   de 6 dígitos con `autocomplete="one-time-code"`, que se envía al completar el sexto dígito, y un
   enlace "Usar un código de respaldo".
2. **Dado** Mi cuenta › Seguridad, **cuando** activo 2FA, **entonces** un `UStepper` guía: escanear QR
   (con la clave en texto como alternativa accesible) → verificar código → guardar códigos de
   respaldo (descargar/copiar) con confirmación explícita "Los he guardado".
3. **Dado** Mi cuenta › Seguridad, **cuando** cambio mi contraseña, **entonces** se pide la actual, se
   valida la política en línea y se cierran mis otras sesiones (opción marcada por defecto).

### S02-15 · `usePermission` y reglas de UI
- **Épica:** EP-T3 · **Área:** frontend · **Pts:** 2

1. **Dado** los permisos de `/me`, **cuando** uso `usePermission('devices.update')` o la directiva
   equivalente, **entonces** obtengo un booleano reactivo.
2. **Dado** el middleware de rutas, **cuando** una página declara `meta.permission`, **entonces** sin
   permiso se muestra 403 dentro del layout.
3. **Dado** un 403 del backend aunque la UI mostrara la acción, **cuando** ocurre, **entonces** se
   muestra "No tienes permiso para esta acción" y se refrescan los permisos.

### S02-16 · Backup de PostgreSQL
- **Épica:** EP-01, EP-19 · **Área:** infra · **Pts:** 3

1. **Dado** el compose, **cuando** pasan 24 h, **entonces** existe un backup de PostgreSQL en MinIO con
   checksum y retención según [`../disaster-recovery.md`](../disaster-recovery.md).
2. **Dado** un backup, **cuando** corre el job semanal de verificación en CI, **entonces** se restaura
   en una instancia limpia y un test comprueba el número de usuarios y eventos de auditoría.

### S02-17 · Pruebas de matriz de permisos
- **Épica:** EP-T2, EP-T4 · **Pts:** 3

1. **Dado** la tabla de rutas y roles, **cuando** corre CI, **entonces** un test genera las
   combinaciones rol × endpoint y comprueba 2xx/403 esperados.

### S02-18 · Preparación ClickHouse
- **Épica:** EP-10 · **Área:** data · **Pts:** 3 · **Prio:** Could

1. **Dado** la decisión C-03 del roadmap, **cuando** se toma, **entonces** el flujo Datos tiene un
   perfil de compose con ClickHouse y la primera migración de [`../database.md`](../database.md)
   aplicada, sin que ningún servicio de S2 dependa de él.

## Riesgos del sprint

| Riesgo | Mitigación |
| --- | --- |
| TOTP y auditoría consumen más de lo previsto | S02-04 y S02-09 son Should: salen primero |
| Contrato de permisos cambia a mitad de sprint | Catálogo cerrado el día 2; cambios posteriores vía PR con aprobación de Frontend |
| Auditoría síncrona penaliza latencia | Outbox + consumidor asíncrono desde el inicio |
