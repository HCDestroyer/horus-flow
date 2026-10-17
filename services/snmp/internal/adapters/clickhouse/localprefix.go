package clickhouse

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// trafficByLocalPrefixSQL agrega, con las mismas claves que trafficByCountrySQL, el volumen hacia redes remotas
// dentro de la lista local por prefijos del ISP (flows.site_remote_net_1d: red /24 o /48). El filtro son
// comparaciones de IPv6 contra los extremos de cada prefijo (arrayExists): barato y aplicado al consultar.
const trafficByLocalPrefixSQL = `SELECT site_id, bucket AS d, remote_asn = 0 AS no_asn, has(?, remote_asn) AS local,
  dictGetOrDefault('dim.organization_dict', 'country',
    tuple(dictGetOrDefault('dim.asn_dict', 'org_id', toUInt64(remote_asn), toUUID('00000000-0000-0000-0000-000000000000'))), '') AS country,
  sumIf(bytes, direction = 'download') AS down, sumIf(bytes, direction = 'upload') AS up
FROM flows.site_remote_net_1d
WHERE tenant_id = ? AND bucket >= toDate(?) AND bucket < toDate(?)
  AND (length(?) = 0 OR has(arrayMap(x -> toUUID(x), ?), site_id))
  AND arrayExists((lo, hi) -> remote_net BETWEEN lo AND hi, arrayMap(x -> toIPv6(x), ?), arrayMap(x -> toIPv6(x), ?))
GROUP BY site_id, d, no_asn, local, country`

// TrafficByLocalPrefix devuelve el volumen hacia la lista local por prefijos en [from, to) agregado como
// TrafficByCountry (para reclasificarlo con TrafficSplit.MoveToPrefix). Sin prefijos no consulta.
func (c *Capacity) TrafficByLocalPrefix(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, localASNs []uint32,
	prefixes []netip.Prefix, from, to time.Time) ([]domain.ASNVolume, error) {
	if len(prefixes) == 0 {
		return nil, nil
	}
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	ss := uuidStrings(sites)
	if localASNs == nil {
		localASNs = []uint32{}
	}
	lo, hi := domain.LocalPrefixRanges(prefixes)
	rows, err := c.conn.Query(ctx, trafficByLocalPrefixSQL, localASNs, tenant, from.UTC(), to.UTC(), ss, ss, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("snmp: traffic by local prefix: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.ASNVolume
	for rows.Next() {
		var v domain.ASNVolume
		if err := rows.Scan(&v.SiteID, &v.Day, &v.NoASN, &v.Local, &v.Country, &v.BytesDown, &v.BytesUp); err != nil {
			return nil, fmt.Errorf("snmp: scan local prefix traffic: %w", err)
		}
		v.Day = v.Day.UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
