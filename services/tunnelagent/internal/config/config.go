// Package config es la configuración del módulo tunnel-agent
// (HORUS_TUNNELAGENT_* y las comunes de mTLS; docs/conventions.md §2.3).
package config

import (
	"log/slog"
	"time"
)

// Drivers.
const (
	DriverKernel = "kernel" // netlink (XFRM, GRE, ipset) + vici (strongSwan)
	DriverMemory = "memory" // en memoria (desarrollo/tests, sin NET_ADMIN)
)

// Config del módulo.
type Config struct {
	Driver string `env:"HORUS_TUNNELAGENT_DRIVER" envDefault:"kernel"`
	HubID  string `env:"HORUS_WG_HUB_ID"`
	// ViciSocket es el socket del demonio IKE (contenedor horus-ipsec).
	ViciSocket string `env:"HORUS_TUNNELAGENT_VICI_SOCKET" envDefault:"/run/horus-ipsec/charon.vici"`
	// Underlay: interfaz física de las XFRM (vacía = la de la ruta por defecto).
	Underlay string `env:"HORUS_TUNNELAGENT_UNDERLAY_INTERFACE"`
	// Conjuntos ipset de horus-tunnel.
	GRESet    string `env:"HORUS_TUNNELAGENT_GRE_SET" envDefault:"horus-gre-peers"`
	SourceSet string `env:"HORUS_TUNNELAGENT_SOURCE_SET" envDefault:"horus-tunnel-src"`
	// GRPCAddr sirve TunnelAgentService (solo lo llama wireguard por mTLS).
	GRPCAddr string `env:"HORUS_TUNNELAGENT_GRPC_ADDR" envDefault:":9096"`
	// ControlAddr es el TunnelControlService de wireguard si no está en el proceso.
	ControlAddr string        `env:"HORUS_WIREGUARD_GRPC_ADDR_REMOTE"`
	ReportEvery time.Duration `env:"HORUS_TUNNELAGENT_REPORT_EVERY" envDefault:"15s"`
	TLSMode     string        `env:"GRPC_TLS_MODE" envDefault:"mtls"`
	TLSCAFile   string        `env:"HORUS_TLS_CA_FILE"`
	TLSCertFile string        `env:"HORUS_TLS_CERT_FILE"`
	TLSKeyFile  string        `env:"HORUS_TLS_KEY_FILE"`
}

// LogValue implementa slog.LogValuer (sin secretos: el agente no tiene ninguno propio).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.String("driver", c.Driver), slog.String("vici_socket", c.ViciSocket),
		slog.String("underlay", c.Underlay), slog.String("tls", c.TLSMode))
}
