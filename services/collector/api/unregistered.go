package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// UnregisteredBucket guarda las IPs que envían flujos sin ser exportador
// registrado (datagramas descartados, métrica dropped{reason=unknown_exporter}).
// Lo escribe el collector y lo lee el rol ingester en
// GET /platform/exporters/unregistered (OpenAPI platform.yaml).
const UnregisteredBucket = "flow_exporter_unregistered"

// UnregisteredTTL es lo que dura una entrada sin datagramas nuevos (igual que
// el olvido en memoria del collector).
const UnregisteredTTL = time.Hour

// UnregisteredKVConfig es la configuración del bucket (la misma en collector,
// ingester e infrastructure/nats/kv.yaml). max_bytes acota el bucket ante
// orígenes falsificados: al llenarse se rechazan las escrituras nuevas.
func UnregisteredKVConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: UnregisteredBucket,
		Description: "Exportadores no registrados que envían flujos (descartados)", History: 1,
		TTL: UnregisteredTTL, MaxBytes: 16 << 20, Storage: jetstream.FileStorage}
}

// Unregistered es el valor del bucket por (collector, IP). Solo lleva lo que
// el contrato expone más la instancia del collector (para sumar varias).
type Unregistered struct {
	ExporterIP  string    `json:"exporter_ip"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Datagrams   uint64    `json:"datagrams"`
	Collector   string    `json:"collector"`
}

// UnregisteredKey es la clave KV de ip vista por collector (las claves NATS
// no admiten ':' de IPv6 ni otros símbolos).
func UnregisteredKey(collector string, ip netip.Addr) string {
	return kvToken(collector) + "." + kvToken(ip.Unmap().String())
}

func kvToken(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '=':
			return r
		default:
			return '_'
		}
	}, s)
}

// UnregisteredLister lista las IPs no registradas (agregadas por IP).
type UnregisteredLister interface {
	ListUnregistered(ctx context.Context) ([]Unregistered, error)
}

// KVUnregistered adapta el bucket flow_exporter_unregistered.
type KVUnregistered struct{ KV jetstream.KeyValue }

// ListUnregistered implementa UnregisteredLister.
func (k KVUnregistered) ListUnregistered(ctx context.Context) ([]Unregistered, error) {
	lister, err := k.KV.ListKeys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = lister.Stop() }()
	var rows []Unregistered
	for key := range lister.Keys() {
		e, err := k.KV.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			continue // caducó entre el listado y la lectura
		}
		if err != nil {
			return nil, err
		}
		var u Unregistered
		if json.Unmarshal(e.Value(), &u) != nil {
			continue
		}
		rows = append(rows, u)
	}
	return MergeUnregistered(rows), nil
}

// MergeUnregistered suma lo visto por cada collector: primera y última vez
// extremas y datagramas acumulados, una fila por IP válida. Orden: última
// vez descendente y, a igualdad, IP.
func MergeUnregistered(rows []Unregistered) []Unregistered {
	by := map[netip.Addr]*Unregistered{}
	for _, r := range rows {
		ip, err := netip.ParseAddr(r.ExporterIP)
		if err != nil {
			continue
		}
		ip = ip.Unmap()
		cur := by[ip]
		if cur == nil {
			by[ip] = &Unregistered{ExporterIP: ip.String(), FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt, Datagrams: r.Datagrams}
			continue
		}
		if r.FirstSeenAt.Before(cur.FirstSeenAt) {
			cur.FirstSeenAt = r.FirstSeenAt
		}
		if r.LastSeenAt.After(cur.LastSeenAt) {
			cur.LastSeenAt = r.LastSeenAt
		}
		cur.Datagrams += r.Datagrams
	}
	out := make([]Unregistered, 0, len(by))
	for _, u := range by {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeenAt.Equal(out[j].LastSeenAt) {
			return out[i].LastSeenAt.After(out[j].LastSeenAt)
		}
		return out[i].ExporterIP < out[j].ExporterIP
	})
	return out
}
