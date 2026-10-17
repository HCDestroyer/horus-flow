package snmpclient

import (
	"context"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Fabricantes soportados (devices.router.vendor).
const (
	VendorMikroTik = "mikrotik"
	VendorHuawei   = "huawei"
)

// Adapter es lo que cambia por fabricante en el sondeo SNMP. El núcleo
// estándar (IF-MIB: Discover y ReadCounters) es común; cada adaptador decide
// cómo se leen el modelo, la CPU, la memoria y la salud, y qué interfaces se
// sondean cada intervalo (docs/vendors/mikrotik.md §3, docs/vendors/huawei.md §3).
type Adapter interface {
	// Vendor es el nombre del fabricante (etiqueta de métricas).
	Vendor() string
	ReadSystem(ctx context.Context, t Transport) (System, error)
	ReadResources(ctx context.Context, t Transport) (Resources, error)
	ReadHealth(ctx context.Context, t Transport) (Health, error)
	// Monitored decide si una interfaz descubierta se sondea cada intervalo.
	Monitored(ifType int, name string) bool
}

// ForVendor devuelve el adaptador del fabricante; uno desconocido (o vacío)
// usa el de MikroTik, el comportamiento anterior a los adaptadores.
func ForVendor(v string) Adapter {
	if strings.EqualFold(strings.TrimSpace(v), VendorHuawei) {
		return Huawei{}
	}
	return MikroTik{}
}

// Discover y ReadCounters son el núcleo estándar (IF-MIB), iguales para todos.

// MikroTik es el adaptador de RouterOS: HOST-RESOURCES-MIB + MIKROTIK-MIB.
type MikroTik struct{}

var _ Adapter = MikroTik{}

// Vendor implementa Adapter.
func (MikroTik) Vendor() string { return VendorMikroTik }

// ReadSystem implementa Adapter.
func (MikroTik) ReadSystem(ctx context.Context, t Transport) (System, error) {
	return ReadSystem(ctx, t)
}

// ReadResources implementa Adapter.
func (MikroTik) ReadResources(ctx context.Context, t Transport) (Resources, error) {
	return ReadResources(ctx, t)
}

// ReadHealth implementa Adapter.
func (MikroTik) ReadHealth(ctx context.Context, t Transport) (Health, error) {
	return ReadHealth(ctx, t)
}

// Monitored implementa Adapter (docs/vendors/mikrotik.md §3.3).
func (MikroTik) Monitored(ifType int, name string) bool { return domain.Monitored(ifType, name) }
