package app

import (
	"context"
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Reparto con la lista local por prefijos: el volumen hacia esas redes pasa
// de su clase por ASN a local (total, por nodo y por día) y los ajustes avisan
// de solapes con prefijos de clientes.
func TestTrafficSplitLocalPrefixes(t *testing.T) {
	f := newCapFixture(t)
	series := newFakeSeries(func(uuid.UUID, time.Time) float64 { return 0 })
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	series.traffic = []domain.ASNVolume{
		{SiteID: f.site, Day: day, Country: "US", BytesDown: 700},
		{SiteID: f.site, Day: day, Country: "GT", BytesDown: 200},
		{SiteID: f.site, Day: day, NoASN: true, BytesUp: 100},
	}
	series.byPrefix = []domain.ASNVolume{
		{SiteID: f.site, Day: day, Country: "US", BytesDown: 300},
		{SiteID: f.site, Day: day, NoASN: true, BytesUp: 100},
	}
	st := domain.DefaultCapacitySettings()
	st.LocalPrefixes = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	f.repo.settings[f.tenant] = domain.SettingsRow{Settings: st, Version: 1}
	c := f.capacity(series, nil)
	c.o.ClientPrefixes = func(uuid.UUID) []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("198.51.100.128/25")} }
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := c.TrafficSplit(context.Background(), f.tenant, nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	tot := v.Total
	if v.LocalPrefixes != 1 || tot.InternationalBytes != 400 || tot.LocalBytes != 600 || tot.LocalByPrefix != 400 || tot.UnknownBytes != 0 ||
		math.Abs(*tot.InternationalShare-0.4) > 1e-9 {
		t.Fatalf("total = %+v", tot)
	}
	if v.Sites[0].LocalByPrefix != 400 || v.Daily[0].LocalBytes != 600 || v.Daily[0].InternationalBytes != 400 {
		t.Fatalf("por nodo / día = %+v %+v", v.Sites[0], v.Daily[0])
	}
	sv, err := c.GetSettings(context.Background(), f.tenant)
	if err != nil || len(sv.LocalPrefixWarnings) != 1 || sv.LocalPrefixWarnings[0].Code != domain.WarnOverlapsClientPrefix {
		t.Fatalf("avisos = %+v %v", sv.LocalPrefixWarnings, err)
	}
}
