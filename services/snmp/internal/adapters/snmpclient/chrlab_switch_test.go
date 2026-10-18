//go:build chrlab

package snmpclient

// Switch en modo directo contra el CHR REAL del laboratorio: la dirección de gestión es la IP del CHR en la WAN
// del laboratorio (sin túnel) y la credencial es la de la configuración de switch que genera Horus
// (BuildSwitchConfig, pegada con /rest/execute). Comprueba con el transporte real SNMPv3 (SHA1+AES) y SNMP v2c
// (la ruta de SwOS) el descubrimiento IF-MIB y los vecinos LLDP.
//
//	HORUS_CHR_SWITCH=198.18.0.2 HORUS_CHR_SWITCH_USER=horus-0000lab1 HORUS_CHR_SWITCH_AUTH=… HORUS_CHR_SWITCH_PRIV=… \
//	HORUS_CHR_SWITCH_COMMUNITY=… go test -tags chrlab -run TestCHRSwitch -v ./services/snmp/internal/adapters/snmpclient/

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestCHRSwitch(t *testing.T) {
	host := os.Getenv("HORUS_CHR_SWITCH")
	if host == "" {
		t.Skip("HORUS_CHR_SWITCH no definido (laboratorio CHR apagado)")
	}
	ctx := context.Background()
	d := GoSNMP{Timeout: 10 * time.Second, Retries: 1}
	creds := map[string]Credential{
		"v3": {Version: "3", Username: os.Getenv("HORUS_CHR_SWITCH_USER"), AuthProtocol: "sha1", AuthPassword: os.Getenv("HORUS_CHR_SWITCH_AUTH"),
			PrivProtocol: "aes128", PrivPassword: os.Getenv("HORUS_CHR_SWITCH_PRIV")},
		"v2c": {Version: "2c", Community: os.Getenv("HORUS_CHR_SWITCH_COMMUNITY")},
	}
	for name, c := range creds {
		t.Run(name, func(t *testing.T) {
			if c.Username == "" && c.Community == "" {
				t.Skip("sin credencial")
			}
			tr, err := d.Dial(ctx, netip.MustParseAddr(host), c)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tr.Close() }()
			a := ForDevice("mikrotik", PlatformRouterOS)
			if sys, err := a.ReadSystem(ctx, tr); err != nil || sys.UptimeTicks == nil {
				t.Fatalf("sistema: %+v %v", sys, err)
			}
			disc, err := Discover(ctx, tr)
			if err != nil || len(disc) == 0 {
				t.Fatalf("descubrimiento: %v %v", disc, err)
			}
			nb, err := ReadNeighbors(ctx, tr, disc)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%d interfaces, vecinos: %+v", len(disc), nb)
		})
	}
}
