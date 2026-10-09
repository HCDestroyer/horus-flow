// Package postgres es el repositorio PostgreSQL de detection (esquema
// `detection`). Todas las operaciones de negocio van en pgdb.TenantTx (RLS
// fail-closed); solo el planificador del motor enumera tenants con
// PlatformTx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// ErrNotFound indica que la fila no existe en el tenant.
var ErrNotFound = errors.New("detection: not found")

// Store es el repositorio.
type Store struct{ DB *pgdb.DB }

// New crea el repositorio.
func New(db *pgdb.DB) *Store { return &Store{DB: db} }

const findingCols = `id, tenant_id, version, state, kind, category, severity, confidence, customer_id, realm_id, site_id, router_id,
	address, customer_kind, target_type, target_value, signals, summary, reasons, evidence, window_from, window_to, first_seen_at,
	last_seen_at, opened_at, updated_at, occurrences, rule_version, reputation_snapshot_version, min_sampling_rate,
	sampling_reduced_confidence, previous_finding_id, acknowledged_by, acknowledged_at, resolution, resolved_at, silence_until`

// ScanFinding lee una fila con findingCols.
func ScanFinding(row pgx.Row) (*domain.Finding, error) {
	var f domain.Finding
	var summary, reasons, evidence, resolution []byte
	var addr netip.Prefix
	err := row.Scan(&f.ID, &f.TenantID, &f.Version, &f.State, &f.Kind, &f.Category, &f.Severity, &f.Confidence, &f.CustomerID,
		&f.RealmID, &f.SiteID, &f.RouterID, &addr, &f.CustomerKind, &f.Target.Type, &f.Target.Value, &f.Signals, &summary, &reasons,
		&evidence, &f.WindowFrom, &f.WindowTo, &f.FirstSeenAt, &f.LastSeenAt, &f.OpenedAt, &f.UpdatedAt, &f.Occurrences, &f.RuleVersion,
		&f.ReputationSnapshotVersion, &f.MinSamplingRate, &f.SamplingReducedConfidence, &f.PreviousFindingID, &f.AcknowledgedBy,
		&f.AcknowledgedAt, &resolution, &f.ResolvedAt, &f.SilenceUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.Address = addr
	if err := json.Unmarshal(summary, &f.Summary); err != nil {
		return nil, fmt.Errorf("summary: %w", err)
	}
	if err := json.Unmarshal(reasons, &f.Reasons); err != nil {
		return nil, fmt.Errorf("reasons: %w", err)
	}
	if err := json.Unmarshal(evidence, &f.Evidence); err != nil {
		return nil, fmt.Errorf("evidence: %w", err)
	}
	if len(resolution) > 0 && string(resolution) != "null" {
		f.Resolution = &domain.Resolution{}
		if err := json.Unmarshal(resolution, f.Resolution); err != nil {
			return nil, fmt.Errorf("resolution: %w", err)
		}
	}
	for _, t := range []*time.Time{&f.WindowFrom, &f.WindowTo, &f.FirstSeenAt, &f.LastSeenAt, &f.OpenedAt, &f.UpdatedAt} {
		*t = t.UTC()
	}
	return &f, nil
}

func collect(rows pgx.Rows) ([]domain.Finding, error) {
	defer rows.Close()
	var out []domain.Finding
	for rows.Next() {
		f, err := ScanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// Get lee un hallazgo (FOR UPDATE si lock).
func Get(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (*domain.Finding, error) {
	q := `SELECT ` + findingCols + ` FROM detection.finding WHERE id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	return ScanFinding(tx.QueryRow(ctx, q, id))
}

// Active devuelve el hallazgo activo de una clave de deduplicación.
func Active(ctx context.Context, tx pgx.Tx, customer uuid.UUID, kind string, t domain.Target) (*domain.Finding, error) {
	return ScanFinding(tx.QueryRow(ctx, `SELECT `+findingCols+` FROM detection.finding
		WHERE customer_id = $1 AND kind = $2 AND target_type = $3 AND target_value = $4 AND state IN ('open', 'acknowledged')
		FOR UPDATE`, customer, kind, t.Type, t.Value))
}

// LastClosed devuelve el último hallazgo cerrado de una clave.
func LastClosed(ctx context.Context, tx pgx.Tx, customer uuid.UUID, kind string, t domain.Target) (*domain.Finding, error) {
	return ScanFinding(tx.QueryRow(ctx, `SELECT `+findingCols+` FROM detection.finding
		WHERE customer_id = $1 AND kind = $2 AND target_type = $3 AND target_value = $4 AND state IN ('resolved', 'false_positive')
		ORDER BY updated_at DESC LIMIT 1`, customer, kind, t.Type, t.Value))
}

func jsonb(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// Insert crea un hallazgo.
func Insert(ctx context.Context, tx pgx.Tx, f *domain.Finding) error {
	var res any
	if f.Resolution != nil {
		res = jsonb(f.Resolution)
	}
	_, err := tx.Exec(ctx, `INSERT INTO detection.finding (`+findingCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37)`,
		f.ID, f.TenantID, f.Version, f.State, f.Kind, f.Category, f.Severity, f.Confidence, f.CustomerID, f.RealmID, f.SiteID, f.RouterID,
		f.Address, f.CustomerKind, f.Target.Type, f.Target.Value, f.Signals, jsonb(f.Summary), jsonb(f.Reasons), jsonb(f.Evidence),
		f.WindowFrom, f.WindowTo, f.FirstSeenAt, f.LastSeenAt, f.OpenedAt, f.UpdatedAt, f.Occurrences, f.RuleVersion,
		f.ReputationSnapshotVersion, f.MinSamplingRate, f.SamplingReducedConfidence, f.PreviousFindingID, f.AcknowledgedBy,
		f.AcknowledgedAt, res, f.ResolvedAt, f.SilenceUntil)
	if err != nil {
		return fmt.Errorf("insert finding: %w", err)
	}
	return nil
}

// Update reescribe un hallazgo si sigue en la versión expect (bloqueo optimista).
func Update(ctx context.Context, tx pgx.Tx, f *domain.Finding, expect int) error {
	var res any
	if f.Resolution != nil {
		res = jsonb(f.Resolution)
	}
	tag, err := tx.Exec(ctx, `UPDATE detection.finding SET version = $2, state = $3, severity = $4, confidence = $5, customer_kind = $6,
		signals = $7, summary = $8, reasons = $9, evidence = $10, window_from = $11, window_to = $12, first_seen_at = $13, last_seen_at = $14,
		updated_at = $15, occurrences = $16, rule_version = $17, reputation_snapshot_version = $18, min_sampling_rate = $19,
		sampling_reduced_confidence = $20, acknowledged_by = $21, acknowledged_at = $22, resolution = $23, resolved_at = $24,
		silence_until = $25, router_id = $26
		WHERE id = $1 AND version = $27`,
		f.ID, f.Version, f.State, f.Severity, f.Confidence, f.CustomerKind, f.Signals, jsonb(f.Summary), jsonb(f.Reasons), jsonb(f.Evidence),
		f.WindowFrom, f.WindowTo, f.FirstSeenAt, f.LastSeenAt, f.UpdatedAt, f.Occurrences, f.RuleVersion, f.ReputationSnapshotVersion,
		f.MinSamplingRate, f.SamplingReducedConfidence, f.AcknowledgedBy, f.AcknowledgedAt, res, f.ResolvedAt, f.SilenceUntil, f.RouterID, expect)
	if err != nil {
		return fmt.Errorf("update finding: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// ListFilter filtra el listado.
type ListFilter struct {
	Kinds, Severities, States []string
	CustomerID                *uuid.UUID
	Sites                     []uuid.UUID // filtro pedido
	AllowedSites              []uuid.UUID // alcance del permiso (nil = todo el tenant)
	OpenedGTE                 *time.Time
	Sort                      string
	Offset, Limit             int
}

// List devuelve una página (limit + 1 filas para saber si hay más).
func List(ctx context.Context, tx pgx.Tx, f ListFilter) ([]domain.Finding, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if len(f.Kinds) > 0 {
		where = append(where, "kind = ANY("+arg(f.Kinds)+")")
	}
	if len(f.Severities) > 0 {
		where = append(where, "severity = ANY("+arg(f.Severities)+")")
	}
	if len(f.States) > 0 {
		where = append(where, "state = ANY("+arg(f.States)+")")
	}
	if f.CustomerID != nil {
		where = append(where, "customer_id = "+arg(*f.CustomerID))
	}
	if f.Sites != nil {
		where = append(where, "site_id = ANY("+arg(f.Sites)+")")
	}
	if f.AllowedSites != nil {
		where = append(where, "site_id = ANY("+arg(f.AllowedSites)+")")
	}
	if f.OpenedGTE != nil {
		where = append(where, "opened_at >= "+arg(*f.OpenedGTE))
	}
	order := map[string]string{
		"":              `CASE severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END DESC, last_seen_at DESC, id`,
		"-severity":     `CASE severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END DESC, last_seen_at DESC, id`,
		"-last_seen_at": "last_seen_at DESC, id", "-opened_at": "opened_at DESC, id", "opened_at": "opened_at, id",
	}[f.Sort]
	q := `SELECT ` + findingCols + ` FROM detection.finding`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY " + order + " OFFSET " + arg(f.Offset) + " LIMIT " + arg(f.Limit+1)
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	return collect(rows)
}

// ActiveOfCustomer devuelve los hallazgos activos de un cliente.
func ActiveOfCustomer(ctx context.Context, tx pgx.Tx, customer uuid.UUID) ([]domain.Finding, error) {
	rows, err := tx.Query(ctx, `SELECT `+findingCols+` FROM detection.finding WHERE customer_id = $1 AND state IN ('open', 'acknowledged')`, customer)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// KnownCustomer indica si detection conoce al cliente en el tenant (tiene
// hallazgos o estado de seguridad). El registro de clientes es de devices:
// un cliente sin historial de seguridad no existe para esta ruta.
func KnownCustomer(ctx context.Context, tx pgx.Tx, customer uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM detection.finding WHERE customer_id = $1)
		OR EXISTS (SELECT 1 FROM detection.customer_security WHERE customer_id = $1)`, customer).Scan(&ok)
	return ok, err
}

// LastResolvedAt devuelve el último cierre por el ISP (verdict resolved) de un cliente.
func LastResolvedAt(ctx context.Context, tx pgx.Tx, customer uuid.UUID) (*time.Time, error) {
	var t *time.Time
	err := tx.QueryRow(ctx, `SELECT max(resolved_at) FROM detection.finding WHERE customer_id = $1 AND state = 'resolved'
		AND resolution->>'verdict' = 'resolved'`, customer).Scan(&t)
	return t, err
}

// Idle devuelve los hallazgos activos sin ocurrencias desde before.
func Idle(ctx context.Context, tx pgx.Tx, before time.Time) ([]domain.Finding, error) {
	rows, err := tx.Query(ctx, `SELECT `+findingCols+` FROM detection.finding WHERE state IN ('open', 'acknowledged') AND last_seen_at < $1
		ORDER BY last_seen_at LIMIT 500 FOR UPDATE`, before)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// SecurityRow es detection.customer_security.
type SecurityRow struct {
	CustomerID, SiteID uuid.UUID
	State              string
	Version            int
	Since              time.Time
}

// GetSecurity lee el estado de seguridad de un cliente.
func GetSecurity(ctx context.Context, tx pgx.Tx, customer uuid.UUID) (*SecurityRow, error) {
	var s SecurityRow
	err := tx.QueryRow(ctx, `SELECT customer_id, site_id, state, version, since FROM detection.customer_security WHERE customer_id = $1 FOR UPDATE`,
		customer).Scan(&s.CustomerID, &s.SiteID, &s.State, &s.Version, &s.Since)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

// PutSecurity inserta o actualiza el estado de seguridad.
func PutSecurity(ctx context.Context, tx pgx.Tx, tenant pgdb.TenantID, s SecurityRow, st domain.SecurityState, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO detection.customer_security (tenant_id, customer_id, site_id, state, version, open_findings, top_kind,
			max_severity, confidence, reasons, since, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (tenant_id, customer_id) DO UPDATE SET site_id = EXCLUDED.site_id, state = EXCLUDED.state, version = EXCLUDED.version,
			open_findings = EXCLUDED.open_findings, top_kind = EXCLUDED.top_kind, max_severity = EXCLUDED.max_severity,
			confidence = EXCLUDED.confidence, reasons = EXCLUDED.reasons, since = EXCLUDED.since, updated_at = EXCLUDED.updated_at`,
		tenant.UUID(), s.CustomerID, s.SiteID, st.State, s.Version, st.OpenFindings, st.TopKind, st.MaxSeverity, st.Confidence,
		jsonb(st.Reasons), s.Since, now)
	return err
}

// InsertVerdict registra la retroalimentación humana (inmutable).
func InsertVerdict(ctx context.Context, tx pgx.Tx, tenant pgdb.TenantID, f *domain.Finding, actor *uuid.UUID) error {
	r := f.Resolution
	_, err := tx.Exec(ctx, `INSERT INTO detection.verdict_feedback (id, tenant_id, finding_id, customer_id, kind, rule_version, verdict,
			comment, silence_until, actions_taken, actor_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uuid.Must(uuid.NewV7()), tenant.UUID(), f.ID, f.CustomerID, f.Kind, f.RuleVersion, r.Verdict, r.Comment, r.SilenceUntil,
		r.ActionsTaken, actor, r.ResolvedAt)
	return err
}

// AllowEntry es una fila de la allowlist.
type AllowEntry struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Prefix    *netip.Prefix
	ASN       *int64
	Kinds     []string
	Reason    string
	CreatedAt time.Time
	CreatedBy uuid.UUID
}

// ListAllow devuelve la allowlist vigente.
func ListAllow(ctx context.Context, tx pgx.Tx, offset, limit int) ([]AllowEntry, error) {
	rows, err := tx.Query(ctx, `SELECT id, tenant_id, prefix, asn, kinds, reason, created_at, created_by FROM detection.reputation_allowlist
		WHERE deleted_at IS NULL ORDER BY created_at, id OFFSET $1 LIMIT $2`, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllowEntry
	for rows.Next() {
		var a AllowEntry
		var p *netip.Prefix
		if err := rows.Scan(&a.ID, &a.TenantID, &p, &a.ASN, &a.Kinds, &a.Reason, &a.CreatedAt, &a.CreatedBy); err != nil {
			return nil, err
		}
		a.Prefix = p
		a.CreatedAt = a.CreatedAt.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// InsertAllow crea una entrada.
func InsertAllow(ctx context.Context, tx pgx.Tx, a AllowEntry) error {
	_, err := tx.Exec(ctx, `INSERT INTO detection.reputation_allowlist (id, tenant_id, prefix, asn, kinds, reason, created_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, a.ID, a.TenantID, a.Prefix, a.ASN, a.Kinds, a.Reason, a.CreatedAt, a.CreatedBy)
	return err
}

// DeleteAllow borra (lógicamente) una entrada.
func DeleteAllow(ctx context.Context, tx pgx.Tx, id uuid.UUID, at time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE detection.reputation_allowlist SET deleted_at = $2 WHERE id = $1 AND deleted_at IS NULL`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DetectorConfig devuelve los overrides por detector.
func DetectorConfig(ctx context.Context, tx pgx.Tx) (map[string]struct {
	Enabled bool
	Params  json.RawMessage
}, error) {
	rows, err := tx.Query(ctx, `SELECT detector, enabled, params FROM detection.detector_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct {
		Enabled bool
		Params  json.RawMessage
	}{}
	for rows.Next() {
		var d string
		var e bool
		var p []byte
		if err := rows.Scan(&d, &e, &p); err != nil {
			return nil, err
		}
		out[d] = struct {
			Enabled bool
			Params  json.RawMessage
		}{e, p}
	}
	return out, rows.Err()
}

// GetState lee una clave de estado del motor.
func GetState(ctx context.Context, tx pgx.Tx, key string) (string, error) {
	var v string
	err := tx.QueryRow(ctx, `SELECT value #>> '{}' FROM detection.engine_state WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetState escribe una clave de estado del motor.
func SetState(ctx context.Context, tx pgx.Tx, tenant pgdb.TenantID, key, value string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO detection.engine_state (tenant_id, key, value, updated_at) VALUES ($1, $2, to_jsonb($3::text), $4)
		ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, tenant.UUID(), key, value, at)
	return err
}

// RegisterTenant anota un tenant para el planificador (idempotente).
func (s *Store) RegisterTenant(ctx context.Context, tenant uuid.UUID) error {
	return s.DB.AppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO detection.tenant_registry (tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, tenant)
		return err
	})
}

// Tenants devuelve los tenants registrados (planificador; proceso de plataforma).
func (s *Store) Tenants(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := s.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id FROM detection.tenant_registry ORDER BY tenant_id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	return out, err
}

// SummaryCounts son los conteos de GET /security/summary.
type SummaryCounts struct {
	OpenBySeverity  map[string]int
	NewLast24h      int
	Affected        int
	BySignal        map[string]int
	ByKind          map[string]int
	BySecurityState map[string]int
	BySite          []SiteCount
}

// SiteCount es una fila por nodo.
type SiteCount struct {
	SiteID       uuid.UUID
	Customers    int
	OpenFindings int
}

// Summary calcula los conteos (sobre hallazgos activos, sin IPs).
func Summary(ctx context.Context, tx pgx.Tx, sites []uuid.UUID, now time.Time) (*SummaryCounts, error) {
	sc := &SummaryCounts{OpenBySeverity: map[string]int{}, BySignal: map[string]int{}, ByKind: map[string]int{}, BySecurityState: map[string]int{}}
	siteCond, args := "", []any{now.Add(-24 * time.Hour)}
	if sites != nil {
		siteCond = " AND site_id = ANY($2)"
		args = append(args, sites)
	}
	active := "state IN ('open', 'acknowledged')" + siteCond
	type kv struct {
		q   string
		dst map[string]int
	}
	for _, x := range []kv{
		{`SELECT severity, count(*) FROM detection.finding WHERE ` + active + ` AND $1::timestamptz IS NOT NULL GROUP BY 1`, sc.OpenBySeverity},
		{`SELECT kind, count(*) FROM detection.finding WHERE ` + active + ` AND $1::timestamptz IS NOT NULL GROUP BY 1`, sc.ByKind},
		{`SELECT s, count(DISTINCT customer_id) FROM detection.finding, unnest(signals) AS s WHERE ` + active + ` AND $1::timestamptz IS NOT NULL GROUP BY 1`, sc.BySignal},
		{`SELECT state, count(*) FROM detection.customer_security WHERE $1::timestamptz IS NOT NULL` + siteCond + ` GROUP BY 1`, sc.BySecurityState},
	} {
		rows, err := tx.Query(ctx, x.q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return nil, err
			}
			x.dst[k] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM detection.finding WHERE opened_at >= $1`+siteCond, args...).Scan(&sc.NewLast24h); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT customer_id) FROM detection.finding WHERE `+active+` AND $1::timestamptz IS NOT NULL`, args...).Scan(&sc.Affected); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT site_id, count(DISTINCT customer_id), count(*) FROM detection.finding WHERE `+active+
		` AND $1::timestamptz IS NOT NULL GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SiteCount
		if err := rows.Scan(&s.SiteID, &s.Customers, &s.OpenFindings); err != nil {
			return nil, err
		}
		sc.BySite = append(sc.BySite, s)
	}
	return sc, rows.Err()
}
