// Package config es la configuración del rol snmp (docs/services.md ficha
// snmp, docs/vendors/mikrotik.md §3.3).
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Config del rol snmp (variables HORUS_SNMP_*).
type Config struct {
	// Enabled: false deja el rol registrado pero sin sondear (meta.poller.enabled=false).
	Enabled bool `env:"HORUS_SNMP_ENABLED" envDefault:"true"`
	// Interval es el sondeo de sistema e interfaces.
	Interval time.Duration `env:"HORUS_SNMP_INTERVAL" envDefault:"60s"`
	// HealthInterval es el sondeo de salud (temperatura, voltaje, sensores).
	HealthInterval time.Duration `env:"HORUS_SNMP_HEALTH_INTERVAL" envDefault:"300s"`
	// ICMPInterval es el ping por el túnel.
	ICMPInterval time.Duration `env:"HORUS_SNMP_ICMP_INTERVAL" envDefault:"30s"`
	// DiscoveryInterval es el redescubrimiento de interfaces (walk de ifName).
	DiscoveryInterval time.Duration `env:"HORUS_SNMP_DISCOVERY_INTERVAL" envDefault:"1h"`
	// Timeout por petición SNMP y Retries reintentos.
	Timeout time.Duration `env:"HORUS_SNMP_TIMEOUT" envDefault:"5s"`
	Retries int           `env:"HORUS_SNMP_RETRIES" envDefault:"1"`
	// Concurrency: routers sondeados a la vez (un poller por router).
	Concurrency int `env:"HORUS_SNMP_CONCURRENCY" envDefault:"32"`

	// Umbrales (%, °C). CPU y memoria sostenidos 3 ciclos; temperatura 1.
	CPUWarning          float64 `env:"HORUS_SNMP_CPU_WARNING" envDefault:"80"`
	CPUCritical         float64 `env:"HORUS_SNMP_CPU_CRITICAL" envDefault:"95"`
	MemoryWarning       float64 `env:"HORUS_SNMP_MEMORY_WARNING" envDefault:"85"`
	MemoryCritical      float64 `env:"HORUS_SNMP_MEMORY_CRITICAL" envDefault:"95"`
	TemperatureWarning  float64 `env:"HORUS_SNMP_TEMPERATURE_WARNING" envDefault:"70"`
	TemperatureCritical float64 `env:"HORUS_SNMP_TEMPERATURE_CRITICAL" envDefault:"85"`

	// ClickHouse (histórico). Vacío: el rol funciona sin series.
	ClickHouseDSN      string `env:"HORUS_CLICKHOUSE_DSN"`
	ClickHousePassword string `env:"HORUS_CLICKHOUSE_PASSWORD"`
	// SNMPPassword: contraseña del usuario horus_snmp (escritor de snmp.*);
	// vacía = se escribe con el usuario del DSN.
	SNMPPassword string `env:"HORUS_CLICKHOUSE_SNMP_PASSWORD"`
	// Migrate aplica las migraciones de snmp.* al arrancar.
	Migrate bool `env:"HORUS_SNMP_CH_MIGRATE" envDefault:"true"`
	// WriterQueue: ciclos pendientes de escritura en ClickHouse antes de descartar.
	WriterQueue int `env:"HORUS_SNMP_CH_QUEUE" envDefault:"64"`

	// InventoryFile: snapshot de inventario (nombres de router y nodo) cuando no hay NATS.
	InventoryFile string `env:"HORUS_FLOWS_INVENTORY_FILE"`
	// PublicKeys/Issuer: validador JWT si auth no es local.
	PublicKeys string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer     string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	// InstanceID identifica el poller en el heartbeat (vacío = HORUS_PROCESS/hostname).
	InstanceID string `env:"HORUS_SNMP_INSTANCE_ID"`

	// PostgreSQL (esquema snmp): umbrales de routers por ISP e inventario de
	// servicios de infraestructura. Sin DSN rigen los umbrales de la
	// instalación y la API de servicios responde 503 (no se vigilan).
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	AppRole          string               `env:"HORUS_SNMP_DB_APP_ROLE" envDefault:"snmp_app"`
	PlatformRole     string               `env:"HORUS_SNMP_DB_PLATFORM_ROLE" envDefault:"snmp_platform"`
	PGMigrate        bool                 `env:"HORUS_SNMP_PG_MIGRATE" envDefault:"true"`
	// InfraConcurrency: servicios sondeados a la vez; InfraTimeout por chequeo activo.
	InfraConcurrency int           `env:"HORUS_SNMP_INFRA_CONCURRENCY" envDefault:"32"`
	InfraTimeout     time.Duration `env:"HORUS_SNMP_INFRA_TIMEOUT" envDefault:"5s"`
	// ServicesCIDR es la red de servicios de Horus en el túnel (la del rol
	// wireguard): origen de los sondeos en el script de router.
	ServicesCIDR string `env:"HORUS_WG_SERVICES_CIDR" envDefault:"10.255.0.0/24"`
	// DockerSubnet es la red de contenedores de la instalación: nunca es
	// objetivo de un chequeo (con la red de servicios del túnel).
	DockerSubnet string `env:"HORUS_DOCKER_SUBNET" envDefault:"172.31.250.0/24"`
	// InfraDenyCIDRs: redes adicionales que no pueden ser objetivo (coma).
	InfraDenyCIDRs []string `env:"HORUS_SNMP_INFRA_DENY_CIDRS" envSeparator:","`
	// PublicBaseURL para Location.
	PublicBaseURL string `env:"HORUS_PUBLIC_BASE_URL"`
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	var errs []error
	for name, d := range map[string]time.Duration{"HORUS_SNMP_INTERVAL": c.Interval, "HORUS_SNMP_HEALTH_INTERVAL": c.HealthInterval,
		"HORUS_SNMP_ICMP_INTERVAL": c.ICMPInterval, "HORUS_SNMP_TIMEOUT": c.Timeout, "HORUS_SNMP_DISCOVERY_INTERVAL": c.DiscoveryInterval} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be > 0", name))
		}
	}
	if c.Retries < 0 {
		errs = append(errs, errors.New("HORUS_SNMP_RETRIES must be >= 0"))
	}
	if c.InfraConcurrency <= 0 {
		errs = append(errs, errors.New("HORUS_SNMP_INFRA_CONCURRENCY must be > 0"))
	}
	if c.InfraTimeout <= 0 {
		errs = append(errs, errors.New("HORUS_SNMP_INFRA_TIMEOUT must be > 0"))
	}
	if c.Concurrency <= 0 {
		errs = append(errs, errors.New("HORUS_SNMP_CONCURRENCY must be > 0"))
	}
	for name, p := range map[string][2]float64{"CPU": {c.CPUWarning, c.CPUCritical}, "MEMORY": {c.MemoryWarning, c.MemoryCritical},
		"TEMPERATURE": {c.TemperatureWarning, c.TemperatureCritical}} {
		if p[0] <= 0 || p[1] <= 0 || p[0] > p[1] {
			errs = append(errs, fmt.Errorf("HORUS_SNMP_%s_WARNING must be > 0 and <= HORUS_SNMP_%s_CRITICAL", name, name))
		}
	}
	return errors.Join(errs...)
}
