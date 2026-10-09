// Package config es la configuración del módulo wg-agent (HORUS_WGAGENT_* y
// las comunes de mTLS; docs/conventions.md §2.3).
package config

import (
	"log/slog"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Drivers de la interfaz.
const (
	DriverKernel = "kernel" // wgctrl (netlink o UAPI de wireguard-go)
	DriverMemory = "memory" // en memoria (desarrollo/tests, sin NET_ADMIN)
)

// Config del módulo.
type Config struct {
	Interface  string `env:"HORUS_WGAGENT_INTERFACE" envDefault:"wg0"`
	ListenPort int    `env:"HORUS_WG_PORT" envDefault:"51820"`
	Driver     string `env:"HORUS_WGAGENT_DRIVER" envDefault:"kernel"`
	// PrivateKey del hub (base64; HORUS_WGAGENT_PRIVATE_KEY_FILE). Nunca sale
	// del agente. Vacía en dev = efímera.
	PrivateKey observability.Secret `env:"HORUS_WGAGENT_PRIVATE_KEY"`
	HubID      string               `env:"HORUS_WG_HUB_ID"`
	// GRPCAddr sirve AgentService (solo lo llama wireguard por mTLS).
	GRPCAddr string `env:"HORUS_WGAGENT_GRPC_ADDR" envDefault:":9095"`
	// ControlAddr es el ControlService de wireguard si no está en el proceso.
	ControlAddr string        `env:"HORUS_WIREGUARD_GRPC_ADDR_REMOTE"`
	ReportEvery time.Duration `env:"HORUS_WGAGENT_REPORT_EVERY" envDefault:"15s"`
	TLSMode     string        `env:"GRPC_TLS_MODE" envDefault:"mtls"`
	TLSCAFile   string        `env:"HORUS_TLS_CA_FILE"`
	TLSCertFile string        `env:"HORUS_TLS_CERT_FILE"`
	TLSKeyFile  string        `env:"HORUS_TLS_KEY_FILE"`
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.String("interface", c.Interface), slog.String("driver", c.Driver),
		slog.Int("listen_port", c.ListenPort), slog.String("tls", c.TLSMode))
}
