package dashboards

import (
	"context"
	"slices"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/securitywidgets"
)

// providerNames son los proveedores en proceso de datos de widgets: tráfico
// (FLOW) y seguridad (SEC, detection). Cada uno declara sus tipos.
var providerNames = []string{tw.ServiceWidgetData, securitywidgets.ServiceWidgetData}

// multiProvider reparte cada tipo al proveedor que lo declara.
type multiProvider []tw.Provider

func (m multiProvider) Types() []string {
	var out []string
	for _, p := range m {
		out = append(out, p.Types()...)
	}
	return out
}

func (m multiProvider) Resolve(ctx context.Context, req tw.Request) (*tw.WidgetData, error) {
	for _, p := range m {
		if slices.Contains(p.Types(), req.Type) {
			return p.Resolve(ctx, req)
		}
	}
	return nil, tw.ErrUnsupportedType
}

// lookupProviders reúne los proveedores registrados en services (los roles
// pueden no estar en este proceso: entonces sus tipos responden 503).
func lookupProviders(services *module.Services) (tw.Provider, bool) {
	var m multiProvider
	for _, name := range providerNames {
		if p, ok := module.Lookup[tw.Provider](services, name); ok {
			m = append(m, p)
		}
	}
	return m, len(m) > 0
}
