package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// ErrTooMany: el ISP ya tiene el máximo de enlaces configurados.
var ErrTooMany = errors.New("snmp: too many uplink configs")

const uplinkCols = `interface_id, router_id, label, provider, kind, contracted_bps, international_egress, updated_at, updated_by, version`

func scanUplink(row pgx.Row) (domain.UplinkConfig, error) {
	var c domain.UplinkConfig
	err := row.Scan(&c.InterfaceID, &c.RouterID, &c.Label, &c.Provider, &c.Kind, &c.ContractedBps, &c.InternationalEgress,
		&c.UpdatedAt, &c.UpdatedBy, &c.Version)
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, err
}

// ListUplinkConfigs devuelve los enlaces configurados del ISP.
func (s *Store) ListUplinkConfigs(ctx context.Context, tenant uuid.UUID) ([]domain.UplinkConfig, error) {
	out := []domain.UplinkConfig{}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+uplinkCols+` FROM snmp.uplink_config WHERE tenant_id = $1 ORDER BY label, interface_id`, tenant)
		if err != nil {
			return fmt.Errorf("snmp: list uplink configs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanUplink(rows)
			if err != nil {
				return fmt.Errorf("snmp: scan uplink config: %w", err)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// GetUplinkConfig devuelve la configuración de un enlace (nil si no tiene).
func (s *Store) GetUplinkConfig(ctx context.Context, tenant, iface uuid.UUID) (*domain.UplinkConfig, error) {
	var out *domain.UplinkConfig
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		c, err := scanUplink(tx.QueryRow(ctx, `SELECT `+uplinkCols+` FROM snmp.uplink_config WHERE tenant_id = $1 AND interface_id = $2`, tenant, iface))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("snmp: get uplink config: %w", err)
		}
		out = &c
		return nil
	})
	return out, err
}

// PutUplinkConfig crea o sustituye la configuración (expect > 0: solo si la
// versión coincide; 0 sin control).
func (s *Store) PutUplinkConfig(ctx context.Context, tenant, iface uuid.UUID, in domain.UplinkConfigInput, actor string, expect int) (*domain.UplinkConfig, error) {
	var out domain.UplinkConfig
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM snmp.uplink_config WHERE tenant_id = $1 AND interface_id <> $2`, tenant, iface).Scan(&n); err != nil {
			return fmt.Errorf("snmp: count uplink configs: %w", err)
		}
		if n >= domain.MaxUplinksPerTenant {
			return ErrTooMany
		}
		var prev *int64
		existed := true
		if err := tx.QueryRow(ctx, `SELECT contracted_bps FROM snmp.uplink_config WHERE tenant_id = $1 AND interface_id = $2 FOR UPDATE`,
			tenant, iface).Scan(&prev); errors.Is(err, pgx.ErrNoRows) {
			existed = false
		} else if err != nil {
			return fmt.Errorf("snmp: lock uplink config: %w", err)
		}
		egress := in.InternationalEgress != nil && *in.InternationalEgress
		c, err := scanUplink(tx.QueryRow(ctx, `INSERT INTO snmp.uplink_config (tenant_id, interface_id, router_id, label, provider, kind,
				contracted_bps, international_egress, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (tenant_id, interface_id) DO UPDATE SET router_id = excluded.router_id, label = excluded.label,
				provider = excluded.provider, kind = excluded.kind, contracted_bps = excluded.contracted_bps,
				international_egress = excluded.international_egress, updated_by = excluded.updated_by, updated_at = now(),
				version = snmp.uplink_config.version + 1
			WHERE $10 = 0 OR snmp.uplink_config.version = $10
			RETURNING `+uplinkCols, tenant, iface, in.RouterID, in.Label, in.Provider, in.Kind, in.ContractedBps, egress, actor, expect))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersion
		}
		if err != nil {
			return fmt.Errorf("snmp: put uplink config: %w", err)
		}
		// Capacidad con vigencia: un cambio (o el alta) abre un periodo nuevo.
		if !existed || !sameBps(prev, in.ContractedBps) {
			from := time.Now().UTC()
			if in.CapacityValidFrom != nil {
				from = in.CapacityValidFrom.UTC()
			}
			if err := recordCapacityChange(ctx, tx, tenant, iface, in.ContractedBps, actor, from); err != nil {
				return err
			}
		}
		out = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUplinkConfig borra la configuración (false si no existía) y el
// estado de sus condiciones.
func (s *Store) DeleteUplinkConfig(ctx context.Context, tenant, iface uuid.UUID) (bool, error) {
	found := false
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM snmp.uplink_config WHERE tenant_id = $1 AND interface_id = $2`, tenant, iface)
		if err != nil {
			return fmt.Errorf("snmp: delete uplink config: %w", err)
		}
		found = tag.RowsAffected() > 0
		if found {
			return closeCapacityPeriod(ctx, tx, tenant, iface)
		}
		return nil
	})
	return found, err
}

func sameBps(a, b *int64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

const settingsCols = `country, planning_threshold, urgent_threshold, lead_days, saturation_warning, saturation_critical, sustained_minutes,
	loss_warning_pct, loss_critical_pct, latency_warning_ms, latency_critical_ms, local_asns, probe_targets, local_prefixes`

func scanSettings(row pgx.Row, extra ...any) (domain.CapacitySettings, error) {
	var v domain.CapacitySettings
	var asns []int64
	var targets []byte
	var country *string
	var prefixes []netip.Prefix
	dst := append([]any{&country, &v.PlanningThreshold, &v.UrgentThreshold, &v.LeadDays, &v.SaturationWarning, &v.SaturationCritical,
		&v.SustainedMinutes, &v.LossWarningPct, &v.LossCriticalPct, &v.LatencyWarningMs, &v.LatencyCriticalMs, &asns, &targets, &prefixes},
		extra...)
	if err := row.Scan(dst...); err != nil {
		return v, err
	}
	v.Country = country
	v.LocalASNs = make([]uint32, 0, len(asns))
	for _, a := range asns {
		if a > 0 && a <= int64(^uint32(0)) {
			v.LocalASNs = append(v.LocalASNs, uint32(a))
		}
	}
	v.LocalPrefixes = make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		v.LocalPrefixes = append(v.LocalPrefixes, p.Masked())
	}
	v.ProbeTargets = []domain.ProbeTarget{}
	if len(targets) > 0 {
		if err := json.Unmarshal(targets, &v.ProbeTargets); err != nil {
			return v, fmt.Errorf("snmp: probe_targets: %w", err)
		}
	}
	return v, nil
}

// GetCapacitySettings devuelve los ajustes del ISP (nil = los de por defecto).
func (s *Store) GetCapacitySettings(ctx context.Context, tenant uuid.UUID) (*domain.SettingsRow, error) {
	var out *domain.SettingsRow
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		r := domain.SettingsRow{}
		v, err := scanSettings(tx.QueryRow(ctx, `SELECT `+settingsCols+`, updated_at, version FROM snmp.capacity_settings WHERE tenant_id = $1`, tenant),
			&r.UpdatedAt, &r.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("snmp: get capacity settings: %w", err)
		}
		r.Settings, r.UpdatedAt = v, r.UpdatedAt.UTC()
		out = &r
		return nil
	})
	return out, err
}

// PutCapacitySettings guarda los ajustes del ISP (expect como PutUplinkConfig).
func (s *Store) PutCapacitySettings(ctx context.Context, tenant uuid.UUID, v domain.CapacitySettings, actor string, expect int) (*domain.SettingsRow, error) {
	r := domain.SettingsRow{Settings: v}
	targets, err := json.Marshal(v.ProbeTargets)
	if err != nil {
		return nil, err
	}
	prefixes := v.LocalPrefixes
	if prefixes == nil {
		prefixes = []netip.Prefix{}
	}
	asns := make([]int64, 0, len(v.LocalASNs))
	for _, a := range v.LocalASNs {
		asns = append(asns, int64(a))
	}
	err = s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO snmp.capacity_settings (tenant_id, `+settingsCols+`, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $17, $15)
			ON CONFLICT (tenant_id) DO UPDATE SET country = excluded.country, planning_threshold = excluded.planning_threshold,
				urgent_threshold = excluded.urgent_threshold, lead_days = excluded.lead_days,
				saturation_warning = excluded.saturation_warning, saturation_critical = excluded.saturation_critical,
				sustained_minutes = excluded.sustained_minutes, loss_warning_pct = excluded.loss_warning_pct,
				loss_critical_pct = excluded.loss_critical_pct, latency_warning_ms = excluded.latency_warning_ms,
				latency_critical_ms = excluded.latency_critical_ms, local_asns = excluded.local_asns,
				probe_targets = excluded.probe_targets, local_prefixes = excluded.local_prefixes, updated_by = excluded.updated_by,
				updated_at = now(),
				version = snmp.capacity_settings.version + 1
			WHERE $16 = 0 OR snmp.capacity_settings.version = $16
			RETURNING updated_at, version`, tenant, v.Country, v.PlanningThreshold, v.UrgentThreshold, v.LeadDays, v.SaturationWarning,
			v.SaturationCritical, v.SustainedMinutes, v.LossWarningPct, v.LossCriticalPct, v.LatencyWarningMs, v.LatencyCriticalMs,
			asns, targets, actor, expect, prefixes).Scan(&r.UpdatedAt, &r.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersion
		}
		if err != nil {
			return fmt.Errorf("snmp: put capacity settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.UpdatedAt = r.UpdatedAt.UTC()
	return &r, nil
}

// DeleteCapacitySettings vuelve a los ajustes por defecto.
func (s *Store) DeleteCapacitySettings(ctx context.Context, tenant uuid.UUID) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM snmp.capacity_settings WHERE tenant_id = $1`, tenant); err != nil {
			return fmt.Errorf("snmp: delete capacity settings: %w", err)
		}
		return nil
	})
}

// CapacityState es lo que el monitor y el trabajo diario cargan de todos los
// ISP (rol de plataforma).
type CapacityState struct {
	Configs    map[uuid.UUID][]domain.UplinkConfig
	Settings   map[uuid.UUID]domain.CapacitySettings
	Conditions []domain.ConditionRow
	Forecasts  []domain.ForecastRow
	// History: capacidad contratada con vigencia por ISP y enlace.
	History map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod
}

// LoadCapacity carga configuraciones, ajustes, condiciones y niveles de
// pronóstico de todos los ISP.
func (s *Store) LoadCapacity(ctx context.Context) (*CapacityState, error) {
	out := &CapacityState{Configs: map[uuid.UUID][]domain.UplinkConfig{}, Settings: map[uuid.UUID]domain.CapacitySettings{},
		History: map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod{}}
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		if err := loadHistory(ctx, tx, out.History); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT tenant_id, `+uplinkCols+` FROM snmp.uplink_config`)
		if err != nil {
			return fmt.Errorf("snmp: load uplink configs: %w", err)
		}
		for rows.Next() {
			var t uuid.UUID
			var c domain.UplinkConfig
			if err := rows.Scan(&t, &c.InterfaceID, &c.RouterID, &c.Label, &c.Provider, &c.Kind, &c.ContractedBps, &c.InternationalEgress,
				&c.UpdatedAt, &c.UpdatedBy, &c.Version); err != nil {
				rows.Close()
				return fmt.Errorf("snmp: scan uplink config: %w", err)
			}
			out.Configs[t] = append(out.Configs[t], c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT tenant_id, `+settingsCols+` FROM snmp.capacity_settings`)
		if err != nil {
			return fmt.Errorf("snmp: load capacity settings: %w", err)
		}
		for rows.Next() {
			var t uuid.UUID
			v, err := scanSettings(rowScanner{rows, &t})
			if err != nil {
				rows.Close()
				return err
			}
			out.Settings[t] = v
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT tenant_id, interface_id, router_id, condition, level, since, value FROM snmp.uplink_condition`)
		if err != nil {
			return fmt.Errorf("snmp: load uplink conditions: %w", err)
		}
		for rows.Next() {
			var c domain.ConditionRow
			if err := rows.Scan(&c.TenantID, &c.InterfaceID, &c.RouterID, &c.Condition, &c.Level, &c.Since, &c.Value); err != nil {
				rows.Close()
				return fmt.Errorf("snmp: scan uplink condition: %w", err)
			}
			c.Since = c.Since.UTC()
			out.Conditions = append(out.Conditions, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT tenant_id, subject_id, subject_kind, site_id, level, computed_at FROM snmp.capacity_forecast`)
		if err != nil {
			return fmt.Errorf("snmp: load forecasts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var f domain.ForecastRow
			if err := rows.Scan(&f.TenantID, &f.SubjectID, &f.SubjectKind, &f.SiteID, &f.Level, &f.ComputedAt); err != nil {
				return fmt.Errorf("snmp: scan forecast: %w", err)
			}
			out.Forecasts = append(out.Forecasts, f)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// rowScanner antepone el tenant_id a las columnas de scanSettings.
type rowScanner struct {
	rows   pgx.Rows
	tenant *uuid.UUID
}

func (r rowScanner) Scan(dst ...any) error { return r.rows.Scan(append([]any{r.tenant}, dst...)...) }

// SaveCondition guarda el nuevo nivel de una condición y su evento (outbox)
// en la misma transacción del tenant.
func (s *Store) SaveCondition(ctx context.Context, c domain.ConditionRow, ev *outbox.Event) error {
	return s.db.TenantTx(ctx, pgdb.TenantID(c.TenantID), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO snmp.uplink_condition (tenant_id, interface_id, condition, router_id, level, since, value)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, interface_id, condition) DO UPDATE SET router_id = excluded.router_id, level = excluded.level,
				since = excluded.since, value = excluded.value, updated_at = now()`,
			c.TenantID, c.InterfaceID, c.Condition, c.RouterID, c.Level, c.Since, c.Value); err != nil {
			return fmt.Errorf("snmp: save uplink condition: %w", err)
		}
		if ev != nil {
			return outbox.Insert(ctx, tx, migrations.Schema, *ev)
		}
		return nil
	})
}

// ListConditions devuelve las condiciones no sanas del ISP.
func (s *Store) ListConditions(ctx context.Context, tenant uuid.UUID) ([]domain.ConditionRow, error) {
	out := []domain.ConditionRow{}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT interface_id, router_id, condition, level, since, value FROM snmp.uplink_condition
			WHERE tenant_id = $1 AND level <> 'ok'`, tenant)
		if err != nil {
			return fmt.Errorf("snmp: list uplink conditions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c := domain.ConditionRow{TenantID: tenant}
			if err := rows.Scan(&c.InterfaceID, &c.RouterID, &c.Condition, &c.Level, &c.Since, &c.Value); err != nil {
				return fmt.Errorf("snmp: scan uplink condition: %w", err)
			}
			c.Since = c.Since.UTC()
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// SaveForecast guarda el último pronóstico de un sujeto y, si cambió de
// nivel, su evento (outbox) en la misma transacción.
func (s *Store) SaveForecast(ctx context.Context, f domain.ForecastRow, ev *outbox.Event) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return s.db.TenantTx(ctx, pgdb.TenantID(f.TenantID), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO snmp.capacity_forecast (tenant_id, subject_id, subject_kind, site_id, level, computed_at, forecast)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, subject_id) DO UPDATE SET subject_kind = excluded.subject_kind, site_id = excluded.site_id,
				level = excluded.level, computed_at = excluded.computed_at, forecast = excluded.forecast`,
			f.TenantID, f.SubjectID, f.SubjectKind, f.SiteID, f.Level, f.ComputedAt, body); err != nil {
			return fmt.Errorf("snmp: save forecast: %w", err)
		}
		if ev != nil {
			return outbox.Insert(ctx, tx, migrations.Schema, *ev)
		}
		return nil
	})
}

// ListForecasts devuelve los pronósticos del ISP.
func (s *Store) ListForecasts(ctx context.Context, tenant uuid.UUID) ([]domain.ForecastRow, error) {
	out := []domain.ForecastRow{}
	err := s.db.TenantTx(ctx, pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT forecast FROM snmp.capacity_forecast WHERE tenant_id = $1`, tenant)
		if err != nil {
			return fmt.Errorf("snmp: list forecasts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var body []byte
			if err := rows.Scan(&body); err != nil {
				return fmt.Errorf("snmp: scan forecast: %w", err)
			}
			var f domain.ForecastRow
			if err := json.Unmarshal(body, &f); err != nil {
				return fmt.Errorf("snmp: decode forecast: %w", err)
			}
			f.TenantID = tenant
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}
