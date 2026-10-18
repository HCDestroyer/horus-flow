package notify

import "testing"

// Un switch vía router con el túnel de su router caído queda «unreachable»
// (ADR-0032): no se avisa (el aviso es el túnel del router) y su vuelta a
// online tampoco se anuncia como recuperación.
func TestMapRouterStateUnreachable(t *testing.T) {
	t.Parallel()
	cases := []struct{ prev, state, want string }{
		{routerOnline, routerUnreachable, ""},
		{routerUnreachable, routerOnline, ""},
		{routerOffline, routerOnline, eventRouterRecovered},
		{routerOnline, routerOffline, eventRouterOffline},
	}
	for _, c := range cases {
		if got := mapRouterState(c.prev, c.state, "via_router_tunnel_down"); got != c.want {
			t.Errorf("%s → %s = %q, quiero %q", c.prev, c.state, got, c.want)
		}
	}
}
