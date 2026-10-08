# 0026 — Modo kiosco como dispositivo registrado

- Estado: Aceptada
- Fecha: 2026-10-08
- Decisores: coordinador, a partir de la propuesta del Agente C (ronda 2) y [D8](../po-decisions.md)
- Detalle: [`api.md`](../api.md) §2.12, [`security.md`](../security.md) §5.5, [`frontend.md`](../frontend.md)

## Contexto

D8 pide dashboards para pantallas de monitoreo del NOC, que se quedan encendidas sin nadie
delante. Una pantalla así necesita una credencial que no caduque cada 10 minutos, pero que no
dé acceso a nada más que lo que muestra.

## Decisión

Cada pantalla es un **kiosco registrado**, ligado a un tenant:

1. Un administrador del ISP genera un código de un solo uso (TTL 10 min). El QR lo lleva en el
   fragmento de la URL, que no llega al servidor ni a los logs.
2. La pantalla canjea el código por una cookie HttpOnly rotativa. Si se reutiliza una cookie ya
   rotada, el kiosco se revoca.
3. La cookie se canjea por un JWT de 10 minutos de **solo lectura** con el `tid` del ISP.
4. El kiosco solo lee los dashboards asignados y los widgets marcados como aptos para kiosco.
   Sin IPs de clientes por defecto; CIDR de origen opcional; revocación inmediata. Cualquier
   otra ruta responde `403 KIOSK_FORBIDDEN`.

## Alternativas consideradas

- **Token largo en la URL:** se filtra por historial, logs y `Referer`; quien lo copie entra
  desde cualquier sitio. Descartado.
- **Sesión de un usuario humano en la pantalla:** mezcla permisos personales con una pantalla
  compartida y caduca. Descartado.

## Consecuencias

- Hace falta gestión de kioscos en la UI (alta, asignación de dashboards, revocación).
- Los widgets declaran si son aptos para kiosco; los que muestran datos personales no lo son por
  defecto.
