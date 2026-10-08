# 0029 — Copias locales siempre, copia remota opcional y paquete de secretos offline

- Estado: Aceptada
- Fecha: 2026-10-08
- Decisores: coordinador, a partir de las propuestas de los Agentes B y C (ronda 2) y [D2](../po-decisions.md)
- Detalle: [`disaster-recovery.md`](../disaster-recovery.md), [`storage.md`](../storage.md),
  [ADR-0019](0019-almacenamiento-local-y-destino-remoto.md)

## Contexto

Con almacenamiento local primario y destino remoto opcional (ADR-0019), las copias deben existir
aunque no haya destino remoto, y la copia remota cifrada solo sirve si la clave de cifrado
sobrevive a la pérdida del servidor.

## Decisión

- **Copias locales siempre**, en un disco distinto del de datos: pgBackRest (posix, WAL continuo)
  y clickhouse-backup.
- **Copia remota opcional** con rclone (SFTP primero; Drive, MEGA, Dropbox después), siempre
  cifrada con `crypt`, también en SFTP. Sin destino configurado, el sistema avisa
  (`RemoteCopyNotConfigured`) pero nada falla.
- **Paquete de secretos offline**: la clave `crypt`, la KEK y la clave del hub WireGuard se
  exportan en un paquete que la persona guarda fuera del servidor. La UI no activa un destino
  remoto hasta que se confirma que el paquete se guardó.
- Restauración de prueba automatizada y periódica.

## Alternativas consideradas

- **Copia remota sin cifrar en SFTP:** más simple, pero expone datos personales si el destino es
  de terceros. Descartado.

## Consecuencias

- RPO de PostgreSQL ≤ 5 min con el disco local; si se pierde el servidor completo, el RPO depende
  de la frecuencia de la copia remota.
- Sin el paquete de secretos no hay recuperación ante desastres: es un procedimiento obligatorio
  del alta de la instalación.
