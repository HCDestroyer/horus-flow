package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/analytics/api/servingrouter"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
)

// ServingRouter implementa servingrouter.Reader sobre flows.flows_raw.
type ServingRouter struct{ Svc *Service }

var _ servingrouter.Reader = ServingRouter{}

// servingRouterSQL usa todo el prefijo de la clave de ordenación de
// flows_raw (tenant_id, site_id, realm_id, client_ip, ts) y una ventana de
// ts: solo lee los gránulos del cliente.
const servingRouterSQL = `SELECT router_id, max(ts) AS last
FROM flows.flows_raw
WHERE tenant_id = ? AND site_id = ? AND realm_id = ? AND client_ip BETWEEN toIPv6(?) AND toIPv6(?)
  AND ts > ? AND ts <= ? AND router_id != toUUID('00000000-0000-0000-0000-000000000000')
GROUP BY router_id
ORDER BY last DESC
LIMIT 1`

// ServingRouter implementa servingrouter.Reader.
func (s ServingRouter) ServingRouter(ctx context.Context, tenant uuid.UUID, q servingrouter.Query) (*servingrouter.Serving, error) {
	if !q.Prefix.IsValid() {
		return nil, fmt.Errorf("%w: prefijo no válido", ErrBadRequest)
	}
	lookback := q.Lookback
	if lookback <= 0 {
		lookback = servingrouter.DefaultLookback
	}
	at := q.At.UTC()
	lo, hi := servingrouter.Range(q.Prefix)
	rows, cancel, err := s.Svc.Q.Query(ctx, tenant, servingRouterSQL, tenant, q.SiteID, q.RealmID, lo.String(), hi.String(),
		at.Add(-lookback), at)
	if err != nil {
		if errors.Is(err, ErrNoBackend) || errors.Is(err, chread.ErrUnavailable) {
			return nil, fmt.Errorf("%w: %v", servingrouter.ErrUnavailable, err) //nolint:errorlint // se envuelve el centinela del contrato
		}
		return nil, err
	}
	defer func() { _ = rows.Close(); cancel() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var out servingrouter.Serving
	var last time.Time
	if err := rows.Scan(&out.RouterID, &last); err != nil {
		return nil, fmt.Errorf("analytics: scan serving router: %w", err)
	}
	out.LastFlowAt = last.UTC()
	return &out, rows.Err()
}
