# Lista fija del agente revisor

Versión de [`docs/conventions.md`](../docs/conventions.md) §6.4. El agente revisor (otra sesión,
otra identidad que el autor) recorre esta lista, deja hallazgos en línea y un resumen, y aprueba
o pide cambios en GitHub. Su revisión alimenta el check requerido `agent-review`
([`workflows/pr-gates.yml`](workflows/pr-gates.yml)): "cambios pedidos" bloquea.

1. **Tarea:** ¿el diff cumple la tarea y solo la tarea? ¿Hay código muerto, TODO sin issue,
   secretos?
2. **Contratos:** ¿cambio compatible? ¿Eventos con `tenant_id` y `Horus-Tenant`? ¿Rutas con
   `scope` y permiso?
3. **Aislamiento:** ¿consultas con `TenantScope`? ¿Claves de caché con `t:<tenant_id>:`? ¿Datos
   de otro tenant alcanzables por algún camino que la batería no cubre (exportaciones, jobs, WS)?
4. **Seguridad y privacidad:** checklist de [`docs/security.md`](../docs/security.md) §15; IPs
   de clientes fuera de logs y métricas.
5. **Observabilidad:** logs, métricas y alertas según
   [`docs/observability.md`](../docs/observability.md) §10.
6. **Pruebas:** ¿prueban el comportamiento o solo la implementación? ¿Casos de error y de
   aislamiento?
7. **Documentación:** README del módulo, ADR si hay decisión, runbook si hay un modo de fallo
   nuevo.

Si autor y revisor discrepan dos rondas, se escala a la persona con ambos argumentos.
