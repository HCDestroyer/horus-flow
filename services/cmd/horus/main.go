// Command horus es el binario único de Horus Flow (ADR-0025).
//
// Qué módulos ejecuta cada proceso se elige con HORUS_ROLES o --roles (lista
// separada por comas, o "all"). El proceso:
//
//   - carga la configuración común HORUS_* (packages/go/config);
//   - escribe logs JSON en stdout con service, role, trace_id, request_id y
//     tenant_id (packages/go/observability);
//   - sirve /healthz, /readyz (estado por rol) y /metrics en HORUS_ADMIN_ADDR;
//   - sirve la API REST de los roles locales en HORUS_HTTP_ADDR (si algún rol
//     registra rutas);
//   - arranca los roles en orden y, al recibir SIGTERM/SIGINT, pone /readyz en
//     503, espera HORUS_SHUTDOWN_DELAY, drena dentro de HORUS_SHUTDOWN_TIMEOUT
//     y sale con código 0. Una segunda señal termina el proceso de inmediato.
//
// Códigos de salida: 0 apagado limpio, 1 fallo en ejecución o apagado, 2
// error de uso o de configuración.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version se inyecta en el build con -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // restaura el comportamiento por defecto: una segunda señal mata el proceso
	}()
	code := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr, roleCatalog)
	stop()
	os.Exit(code)
}
