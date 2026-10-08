package health

import (
	"context"
	"fmt"
	"net"
)

// DialProbe devuelve un Probe que comprueba que addr acepta conexiones en
// network ("tcp", "unix"…). Útil para dependencias sin cliente propio todavía.
func DialProbe(network, addr string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return fmt.Errorf("unreachable: %w", err)
		}
		return c.Close() //nolint:wrapcheck // error de cierre autoexplicativo
	}
}
