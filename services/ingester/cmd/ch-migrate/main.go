// Command ch-migrate aplica el esquema ClickHouse v0 (I0-13) y crea los
// usuarios por módulo. Es el mismo código que ejecuta el rol ingester al
// arrancar; existe para `make migrate-ch` y para jobs de despliegue hasta que
// el binario horus tenga `horus migrate --module=ingester`.
//
// Configuración (variables de entorno, con variante _FILE):
//
//	HORUS_CLICKHOUSE_DSN                 clickhouse://usuario@host:9000/base (obligatoria)
//	HORUS_CLICKHOUSE_PASSWORD            contraseña del migrador
//	HORUS_CLICKHOUSE_<MÓDULO>_PASSWORD   ingester, analytics, detection, alerts, jobs (opcionales)
//
// Sale con 0 si el esquema queda al día y con 1 ante cualquier error.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hcdestroyer/horus-flow/services/ingester"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := ingester.MigrateClickHouse(ctx, os.Environ(), log)
	cancel()
	stop()
	if err != nil {
		log.Error("clickhouse migration failed", "error", err)
		os.Exit(1)
	}
}
