//go:build integration

package analytics_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/analytics/api/servingrouter"
)

// El router que sirve a un cliente es el de sus flujos más recientes (no el
// principal del nodo); un prefijo IPv6 cuenta todas sus direcciones y fuera de
// la ventana no hay respuesta.
func TestServingRouter(t *testing.T) {
	e := setup(t, flowinv.Data{})
	r, ok := module.Lookup[servingrouter.Reader](e.services, servingrouter.ServiceServingRouter)
	if !ok {
		t.Fatal("servingrouter.Reader no registrado")
	}
	backup := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	client := netip.MustParseAddr("10.20.0.41")
	v6 := netip.MustParseAddr("2001:db8:4af0:100::7")
	e.insert([]row{
		{ts: now.Add(-50 * time.Minute), realm: e.realm, status: "attributed", direction: "download", client: client, bytes: 100, packets: 1},
		{ts: now.Add(-5 * time.Minute), realm: e.realm, status: "attributed", direction: "download", client: client, bytes: 100, packets: 1,
			router: backup},
		{ts: now.Add(-3 * time.Minute), realm: e.realm, status: "attributed", direction: "upload", client: v6, bytes: 10, packets: 1},
		// Otro cliente del mismo nodo, más reciente, por el router de respaldo: no cuenta.
		{ts: now.Add(-1 * time.Minute), realm: e.realm, status: "attributed", direction: "download",
			client: netip.MustParseAddr("10.20.0.42"), bytes: 1, packets: 1, router: backup},
	})
	ctx := context.Background()
	got, err := r.ServingRouter(ctx, e.tenant, servingrouter.Query{SiteID: e.site, RealmID: e.realm,
		Prefix: netip.PrefixFrom(client, 32), At: now})
	if err != nil || got == nil || got.RouterID != backup || !got.LastFlowAt.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("cliente IPv4 = %+v %v", got, err)
	}
	got, err = r.ServingRouter(ctx, e.tenant, servingrouter.Query{SiteID: e.site, RealmID: e.realm,
		Prefix: netip.MustParsePrefix("2001:db8:4af0:100::/56"), At: now})
	if err != nil || got == nil || got.RouterID != e.router {
		t.Fatalf("prefijo IPv6 = %+v %v", got, err)
	}
	// Consulta en el pasado: solo cuenta lo anterior a at.
	got, err = r.ServingRouter(ctx, e.tenant, servingrouter.Query{SiteID: e.site, RealmID: e.realm,
		Prefix: netip.PrefixFrom(client, 32), At: now.Add(-30 * time.Minute)})
	if err != nil || got == nil || got.RouterID != e.router {
		t.Fatalf("en el pasado = %+v %v", got, err)
	}
	// Sin flujos en la ventana (o de otro tenant): nil, nunca un router inventado.
	got, err = r.ServingRouter(ctx, e.tenant, servingrouter.Query{SiteID: e.site, RealmID: e.realm,
		Prefix: netip.PrefixFrom(client, 32), At: now, Lookback: time.Minute})
	if err != nil || got != nil {
		t.Fatalf("ventana vacía = %+v %v", got, err)
	}
	if got, err = r.ServingRouter(ctx, uuid.New(), servingrouter.Query{SiteID: e.site, RealmID: e.realm,
		Prefix: netip.PrefixFrom(client, 32), At: now}); err != nil || got != nil {
		t.Fatalf("otro tenant = %+v %v", got, err)
	}
}
