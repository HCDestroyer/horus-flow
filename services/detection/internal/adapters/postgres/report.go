package postgres

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReportCount son los conteos de un (kind, severity) en un periodo.
type ReportCount struct {
	Kind, Severity                  string
	Opened, Resolved, FalsePositive int
}

// ReportCounts cuenta, por tipo y severidad, los hallazgos abiertos,
// resueltos y marcados falso positivo en [from, to) (reporte semanal, I3).
// resolved_at lo fijan ambos veredictos.
func ReportCounts(ctx context.Context, tx pgx.Tx, from, to time.Time) ([]ReportCount, error) {
	rows, err := tx.Query(ctx, `SELECT kind, severity,
			count(*) FILTER (WHERE opened_at >= $1 AND opened_at < $2),
			count(*) FILTER (WHERE state = 'resolved' AND resolved_at >= $1 AND resolved_at < $2),
			count(*) FILTER (WHERE state = 'false_positive' AND resolved_at >= $1 AND resolved_at < $2)
		FROM detection.finding
		WHERE (opened_at >= $1 AND opened_at < $2) OR (resolved_at >= $1 AND resolved_at < $2)
		GROUP BY 1, 2 ORDER BY 1, 2`, from, to)
	if err != nil {
		return nil, fmt.Errorf("report counts: %w", err)
	}
	defer rows.Close()
	var out []ReportCount
	for rows.Next() {
		var c ReportCount
		if err := rows.Scan(&c.Kind, &c.Severity, &c.Opened, &c.Resolved, &c.FalsePositive); err != nil {
			return nil, fmt.Errorf("report counts: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReportMTTR es la media en segundos de opened_at → resolved_at de los
// hallazgos resueltos (veredicto resolved) en [from, to) y cuántos son.
func ReportMTTR(ctx context.Context, tx pgx.Tx, from, to time.Time) (*float64, int, error) {
	var avg *float64
	var n int
	err := tx.QueryRow(ctx, `SELECT avg(extract(epoch FROM resolved_at - opened_at))::float8, count(*)
		FROM detection.finding WHERE state = 'resolved' AND resolved_at >= $1 AND resolved_at < $2`, from, to).Scan(&avg, &n)
	if err != nil {
		return nil, 0, fmt.Errorf("report mttr: %w", err)
	}
	return avg, n, nil
}

// ReportActive cuenta los hallazgos activos (open o acknowledged).
func ReportActive(ctx context.Context, tx pgx.Tx) (int, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM detection.finding WHERE state IN ('open', 'acknowledged')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("report active: %w", err)
	}
	return n, nil
}

// ReportCustomer es un cliente con hallazgos activos.
type ReportCustomer struct {
	CustomerID, RealmID, SiteID uuid.UUID
	Address                     netip.Prefix
	OpenFindings                int
	MaxSeverity, TopKind        string
}

// ReportTopCustomers devuelve hasta limit clientes con más hallazgos activos
// (desempate por la severidad máxima); dirección, realm y nodo del hallazgo
// más reciente.
func ReportTopCustomers(ctx context.Context, tx pgx.Tx, limit int) ([]ReportCustomer, error) {
	rows, err := tx.Query(ctx, `SELECT customer_id,
			(array_agg(realm_id ORDER BY last_seen_at DESC))[1],
			(array_agg(site_id ORDER BY last_seen_at DESC))[1],
			(array_agg(address ORDER BY last_seen_at DESC))[1],
			count(*),
			max(CASE severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END),
			mode() WITHIN GROUP (ORDER BY kind)
		FROM detection.finding WHERE state IN ('open', 'acknowledged')
		GROUP BY customer_id ORDER BY 5 DESC, 6 DESC, 1 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("report top customers: %w", err)
	}
	defer rows.Close()
	sev := map[int]string{4: "critical", 3: "high", 2: "medium", 1: "low"}
	var out []ReportCustomer
	for rows.Next() {
		var c ReportCustomer
		var rank int
		if err := rows.Scan(&c.CustomerID, &c.RealmID, &c.SiteID, &c.Address, &c.OpenFindings, &rank, &c.TopKind); err != nil {
			return nil, fmt.Errorf("report top customers: %w", err)
		}
		c.MaxSeverity = sev[rank]
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReportSecurityStates cuenta los clientes por estado de seguridad actual.
func ReportSecurityStates(ctx context.Context, tx pgx.Tx) (map[string]int, error) {
	out := map[string]int{}
	rows, err := tx.Query(ctx, `SELECT state, count(*) FROM detection.customer_security GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("report security states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("report security states: %w", err)
		}
		out[k] = n
	}
	return out, rows.Err()
}

// ReportReputationHits cuenta los hallazgos abiertos en [from, to) por
// contacto con destinos de reputación (C2 o reputation_hit) y los clientes afectados.
func ReportReputationHits(ctx context.Context, tx pgx.Tx, from, to time.Time) (findings, customers int, err error) {
	err = tx.QueryRow(ctx, `SELECT count(*), count(DISTINCT customer_id) FROM detection.finding
		WHERE opened_at >= $1 AND opened_at < $2
		  AND (kind IN ('botnet_c2_communication', 'reputation_hit') OR 'c2_contact' = ANY(signals))`, from, to).Scan(&findings, &customers)
	if err != nil {
		return 0, 0, fmt.Errorf("report reputation hits: %w", err)
	}
	return findings, customers, nil
}
