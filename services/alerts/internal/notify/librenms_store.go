package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/alerts/internal/librenms"
)

// Integration es el estado de la integración con LibreNMS de un canal
// (alerts.librenms_integration).
type Integration struct {
	ChannelID     uuid.UUID
	TenantID      uuid.UUID
	HorusHostname *string
	CPU           int
	Memory        int
	TZOffset      *int
	RemoteVersion *string
	Status        string
	LastSyncAt    *time.Time
	LastErrorCode *string
	LastError     *string
	Warnings      []string
	Steps         []librenms.Step
	LastTest      *LibreTestReport
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Estados de la integración.
const (
	integrationNotProvisioned = "not_provisioned"
	integrationOK             = "ok"
	integrationPartial        = "partial"
	integrationFailed         = "failed"
)

// LibreTestReport es LibreNmsTestResult (se guarda tal cual como última prueba).
type LibreTestReport struct {
	OK            bool                `json:"ok"`
	ChannelID     uuid.UUID           `json:"channel_id"`
	CheckedAt     string              `json:"checked_at"`
	LatencyMS     *int                `json:"latency_ms"`
	RemoteVersion *string             `json:"remote_version"`
	FailedStep    *string             `json:"failed_step"`
	ErrorCode     *string             `json:"error_code"`
	Error         *string             `json:"error"`
	Line          *string             `json:"line"`
	Steps         []librenms.TestStep `json:"steps"`
}

// RouterConfig es la IP de gestión y el modo de un router para LibreNMS
// (alerts.librenms_router).
type RouterConfig struct {
	TenantID         uuid.UUID
	RouterID         uuid.UUID
	MgmtIP           string
	Mode             string
	SecretCiphertext []byte
	DEKWrapped       []byte
	KEKID            *string
	ConsentAt        *time.Time
	ConsentBy        *string
	UpdatedAt        time.Time
}

// HasSecret indica si hay credenciales SNMP propias guardadas.
func (r *RouterConfig) HasSecret() bool { return len(r.SecretCiphertext) > 0 }

const integrationCols = `channel_id, tenant_id, horus_hostname, cpu_threshold, memory_threshold, tz_offset_minutes, remote_version, status,
	last_sync_at, last_error_code, last_error, warnings, last_sync_steps, last_test, created_at, updated_at`

func scanIntegration(row pgx.Row) (*Integration, error) {
	var in Integration
	var warnings, steps, test []byte
	err := row.Scan(&in.ChannelID, &in.TenantID, &in.HorusHostname, &in.CPU, &in.Memory, &in.TZOffset, &in.RemoteVersion, &in.Status,
		&in.LastSyncAt, &in.LastErrorCode, &in.LastError, &warnings, &steps, &test, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: scan librenms integration: %w", err)
	}
	_ = json.Unmarshal(warnings, &in.Warnings)
	_ = json.Unmarshal(steps, &in.Steps)
	if len(test) > 0 {
		var r LibreTestReport
		if json.Unmarshal(test, &r) == nil {
			in.LastTest = &r
		}
	}
	return &in, nil
}

func (s *Store) getIntegration(ctx context.Context, t pgdb.TenantID, channelID uuid.UUID) (*Integration, []librenms.Object, error) {
	var in *Integration
	var objs []librenms.Object
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		in, err = scanIntegration(tx.QueryRow(ctx, `SELECT `+integrationCols+` FROM alerts.librenms_integration WHERE tenant_id = $1 AND channel_id = $2`,
			t.UUID(), channelID))
		if err != nil {
			return err
		}
		objs, err = listObjects(ctx, tx, t, channelID)
		return err
	})
	return in, objs, err
}

func listObjects(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, channelID uuid.UUID) ([]librenms.Object, error) {
	rows, err := tx.Query(ctx, `SELECT kind, key, name, remote_id, coalesce(hostname, ''), created_by_horus, active FROM alerts.librenms_object
		WHERE tenant_id = $1 AND channel_id = $2 ORDER BY kind, key`, t.UUID(), channelID)
	if err != nil {
		return nil, fmt.Errorf("alerts: list librenms objects: %w", err)
	}
	defer rows.Close()
	var out []librenms.Object
	for rows.Next() {
		var o librenms.Object
		var id int64
		if err := rows.Scan(&o.Kind, &o.Key, &o.Name, &id, &o.Hostname, &o.CreatedByHorus, &o.Active); err != nil {
			return nil, fmt.Errorf("alerts: scan librenms object: %w", err)
		}
		o.RemoteID = librenms.ID(id)
		out = append(out, o)
	}
	return out, rows.Err()
}

// saveIntegration guarda el estado y, si objs != nil, reemplaza los objetos
// conocidos del canal (en la misma transacción).
func (s *Store) saveIntegration(ctx context.Context, t pgdb.TenantID, in *Integration, objs []librenms.Object) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		warnings, _ := json.Marshal(nonNil(in.Warnings))
		steps, _ := json.Marshal(nonNilSteps(in.Steps))
		var test []byte
		if in.LastTest != nil {
			test, _ = json.Marshal(in.LastTest)
		}
		err := tx.QueryRow(ctx, `INSERT INTO alerts.librenms_integration (channel_id, tenant_id, horus_hostname, cpu_threshold, memory_threshold,
			tz_offset_minutes, remote_version, status, last_sync_at, last_error_code, last_error, warnings, last_sync_steps, last_test)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (channel_id) DO UPDATE SET horus_hostname = $3, cpu_threshold = $4, memory_threshold = $5, tz_offset_minutes = $6,
			remote_version = $7, status = $8, last_sync_at = $9, last_error_code = $10, last_error = $11, warnings = $12, last_sync_steps = $13,
			last_test = $14, updated_at = now()
			RETURNING created_at, updated_at`,
			in.ChannelID, t.UUID(), in.HorusHostname, in.CPU, in.Memory, in.TZOffset, in.RemoteVersion, in.Status, in.LastSyncAt,
			in.LastErrorCode, in.LastError, warnings, steps, test).Scan(&in.CreatedAt, &in.UpdatedAt)
		if err != nil {
			return fmt.Errorf("alerts: save librenms integration: %w", err)
		}
		if objs == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM alerts.librenms_object WHERE tenant_id = $1 AND channel_id = $2`, t.UUID(), in.ChannelID); err != nil {
			return fmt.Errorf("alerts: replace librenms objects: %w", err)
		}
		for _, o := range objs {
			var host *string
			if o.Hostname != "" {
				h := o.Hostname
				host = &h
			}
			if _, err := tx.Exec(ctx, `INSERT INTO alerts.librenms_object (tenant_id, channel_id, kind, key, name, remote_id, hostname,
				created_by_horus, active) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				t.UUID(), in.ChannelID, o.Kind, o.Key, o.Name, int64(o.RemoteID), host, o.CreatedByHorus, o.Active); err != nil {
				return fmt.Errorf("alerts: insert librenms object: %w", err)
			}
		}
		return nil
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilSteps(s []librenms.Step) []librenms.Step {
	if s == nil {
		return []librenms.Step{}
	}
	return s
}

func (s *Store) deleteObject(ctx context.Context, t pgdb.TenantID, channelID uuid.UUID, kind, key string) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM alerts.librenms_object WHERE tenant_id = $1 AND channel_id = $2 AND kind = $3 AND key = $4`,
			t.UUID(), channelID, kind, key); err != nil {
			return fmt.Errorf("alerts: delete librenms object: %w", err)
		}
		return nil
	})
}

const routerCols = `tenant_id, router_id, host(mgmt_ip), mode, secret_ciphertext, dek_wrapped, kek_id, reuse_consent_at, reuse_consent_by, updated_at`

func scanRouter(row pgx.Row) (*RouterConfig, error) {
	var r RouterConfig
	err := row.Scan(&r.TenantID, &r.RouterID, &r.MgmtIP, &r.Mode, &r.SecretCiphertext, &r.DEKWrapped, &r.KEKID, &r.ConsentAt, &r.ConsentBy, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: scan librenms router: %w", err)
	}
	return &r, nil
}

func (s *Store) listRouterConfigs(ctx context.Context, t pgdb.TenantID) ([]RouterConfig, error) {
	var out []RouterConfig
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+routerCols+` FROM alerts.librenms_router WHERE tenant_id = $1 ORDER BY router_id`, t.UUID())
		if err != nil {
			return fmt.Errorf("alerts: list librenms routers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRouter(rows)
			if err != nil {
				return err
			}
			out = append(out, *r)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) getRouterConfig(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (*RouterConfig, error) {
	var out *RouterConfig
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanRouter(tx.QueryRow(ctx, `SELECT `+routerCols+` FROM alerts.librenms_router WHERE tenant_id = $1 AND router_id = $2`,
			t.UUID(), routerID))
		return err
	})
	return out, err
}

func (s *Store) upsertRouterConfig(ctx context.Context, t pgdb.TenantID, r *RouterConfig) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO alerts.librenms_router (tenant_id, router_id, mgmt_ip, mode, secret_ciphertext, dek_wrapped, kek_id,
			reuse_consent_at, reuse_consent_by) VALUES ($1, $2, $3::inet, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (tenant_id, router_id) DO UPDATE SET mgmt_ip = $3::inet, mode = $4, secret_ciphertext = $5, dek_wrapped = $6, kek_id = $7,
			reuse_consent_at = $8, reuse_consent_by = $9, updated_at = now() RETURNING updated_at`,
			t.UUID(), r.RouterID, r.MgmtIP, r.Mode, r.SecretCiphertext, r.DEKWrapped, r.KEKID, r.ConsentAt, r.ConsentBy).Scan(&r.UpdatedAt)
		if err != nil {
			return fmt.Errorf("alerts: save librenms router: %w", err)
		}
		return nil
	})
}

func (s *Store) deleteRouterConfig(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM alerts.librenms_router WHERE tenant_id = $1 AND router_id = $2`, t.UUID(), routerID); err != nil {
			return fmt.Errorf("alerts: delete librenms router: %w", err)
		}
		return nil
	})
}
