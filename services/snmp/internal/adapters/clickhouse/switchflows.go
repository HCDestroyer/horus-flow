package clickhouse

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Top de las muestras sFlow de un switch (flows.switch_samples: fuente
// SECUNDARIA, docs/traffic-model.md §13). Solo para la ficha del switch:
// nunca se suma a clientes ni nodos. Lectura con la política p_tenant
// (SQL_horus_tenant) y filtro explícito por tenant.
var switchTopKeys = map[string]string{
	domain.SwitchTopSrc:      "toString(src_ip)",
	domain.SwitchTopDst:      "toString(dst_ip)",
	domain.SwitchTopProtocol: "concat(toString(protocol), '/', toString(dst_port))",
}

const switchTopSQL = `SELECT %s AS k, sum(bytes) AS b, sum(packets) AS p
FROM flows.switch_samples
WHERE tenant_id = ? AND switch_id = ? AND ts >= ? AND ts < ? AND (? = 0 OR input_if_index = ?)
GROUP BY k ORDER BY b DESC, k LIMIT ?`

const switchTotalSQL = `SELECT sum(bytes), count() FROM flows.switch_samples
WHERE tenant_id = ? AND switch_id = ? AND ts >= ? AND ts < ? AND (? = 0 OR input_if_index = ?)`

// SwitchTop devuelve el top por dimensión (src_ip | dst_ip | protocol) de
// las muestras de un switch en [from, to), opcionalmente de un puerto de
// entrada (ifIndex 0 = todos), y el total del periodo.
func (c *Capacity) SwitchTop(ctx context.Context, tenant, switchID uuid.UUID, ifIndex uint32, by string, from, to time.Time,
	limit int) ([]domain.SwitchTopRow, uint64, uint64, error) {
	key, ok := switchTopKeys[by]
	if !ok {
		return nil, 0, 0, fmt.Errorf("snmp: switch top: unknown dimension %q", by)
	}
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, 0, 0, err
	}
	defer cancel()
	var total, samples uint64
	if err := c.conn.QueryRow(ctx, switchTotalSQL, tenant, switchID, from.UTC(), to.UTC(), ifIndex, ifIndex).Scan(&total, &samples); err != nil {
		return nil, 0, 0, fmt.Errorf("snmp: switch top total: %w", err)
	}
	rows, err := c.conn.Query(ctx, fmt.Sprintf(switchTopSQL, key), tenant, switchID, from.UTC(), to.UTC(), ifIndex, ifIndex, limit)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("snmp: switch top: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.SwitchTopRow
	for rows.Next() {
		var r domain.SwitchTopRow
		if err := rows.Scan(&r.Key, &r.Bytes, &r.Packets); err != nil {
			return nil, 0, 0, fmt.Errorf("snmp: scan switch top: %w", err)
		}
		if a, err := netip.ParseAddr(r.Key); err == nil {
			r.Key = a.Unmap().String() // IPv4 mapeada → IPv4
		}
		out = append(out, r)
	}
	return out, total, samples, rows.Err()
}
