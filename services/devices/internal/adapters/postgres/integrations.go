package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var _ app.IntegrationStore = (*Store)(nil)

const connectorCols = `id, tenant_id, base_url, enabled, token_ciphertext, token_dek, secret_ciphertext, secret_dek,
	prev_secret_ciphertext, prev_secret_dek, prev_secret_until, kek_id, resolver, last_sync_at, last_sync_status, last_error,
	failures, backoff_until, created_at, updated_at, version`

func scanConnector(row pgx.Row) (*domain.ArgosConnector, error) {
	var c domain.ArgosConnector
	err := row.Scan(&c.ID, &c.TenantID, &c.BaseURL, &c.Enabled, &c.TokenCiphertext, &c.TokenDEK, &c.SecretCiphertext, &c.SecretDEK,
		&c.PrevSecretCiphertext, &c.PrevSecretDEK, &c.PrevSecretUntil, &c.KEKID, &c.Resolver, &c.LastSyncAt, &c.LastSyncStatus,
		&c.LastError, &c.Failures, &c.BackoffUntil, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// GetArgosConnector implementa app.IntegrationStore.
func (s *Store) GetArgosConnector(ctx context.Context, t pgdb.TenantID) (*domain.ArgosConnector, error) {
	var c *domain.ArgosConnector
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		c, err = scanConnector(tx.QueryRow(ctx, `SELECT `+connectorCols+` FROM devices.integration_argosisp WHERE tenant_id = $1`, t.UUID()))
		return err
	})
	return c, err
}

// ArgosConnectorByID implementa app.IntegrationStore (receptor de webhooks:
// el ISP sale del conector; multi-tenant declarado).
func (s *Store) ArgosConnectorByID(ctx context.Context, id uuid.UUID) (*domain.ArgosConnector, error) {
	var c *domain.ArgosConnector
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		c, err = scanConnector(tx.QueryRow(ctx, `SELECT `+connectorCols+` FROM devices.integration_argosisp WHERE id = $1`, id))
		return err
	})
	return c, err
}

// SaveArgosConnector implementa app.IntegrationStore (expect 0 = alta).
func (s *Store) SaveArgosConnector(ctx context.Context, t pgdb.TenantID, c *domain.ArgosConnector, expect int) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if expect == 0 {
			err := tx.QueryRow(ctx, `INSERT INTO devices.integration_argosisp (id, tenant_id, base_url, enabled, token_ciphertext, token_dek,
				secret_ciphertext, secret_dek, kek_id, created_at, updated_at, version)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now(), 1)
				ON CONFLICT (tenant_id) DO NOTHING RETURNING version, created_at, updated_at`,
				c.ID, t.UUID(), c.BaseURL, c.Enabled, c.TokenCiphertext, c.TokenDEK, c.SecretCiphertext, c.SecretDEK, c.KEKID).
				Scan(&c.Version, &c.CreatedAt, &c.UpdatedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return app.ErrVersionChanged
			}
			return err
		}
		err := tx.QueryRow(ctx, `UPDATE devices.integration_argosisp SET base_url = $3, enabled = $4, token_ciphertext = $5, token_dek = $6,
			secret_ciphertext = $7, secret_dek = $8, prev_secret_ciphertext = $9, prev_secret_dek = $10, prev_secret_until = $11,
			kek_id = $12, failures = $13, backoff_until = $14, updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 AND version = $15 RETURNING version, updated_at`,
			t.UUID(), c.ID, c.BaseURL, c.Enabled, c.TokenCiphertext, c.TokenDEK, c.SecretCiphertext, c.SecretDEK,
			c.PrevSecretCiphertext, c.PrevSecretDEK, c.PrevSecretUntil, c.KEKID, c.Failures, c.BackoffUntil, expect).
			Scan(&c.Version, &c.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrVersionChanged
		}
		return err
	})
}

// RecordArgosSync implementa app.IntegrationStore (no cambia la versión: es estado, no configuración).
func (s *Store) RecordArgosSync(ctx context.Context, t pgdb.TenantID, st app.ArgosSyncState) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE devices.integration_argosisp SET last_sync_at = $2, last_sync_status = $3, last_error = $4,
			resolver = coalesce($5, resolver), failures = $6, backoff_until = $7 WHERE tenant_id = $1`,
			t.UUID(), st.At, st.Status, st.Error, st.Resolver, st.Failures, st.BackoffUntil)
		return err
	})
}

// ActiveArgosTenants implementa app.IntegrationStore (multi-tenant declarado: barrido).
func (s *Store) ActiveArgosTenants(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id FROM devices.integration_argosisp WHERE enabled AND token_ciphertext IS NOT NULL ORDER BY tenant_id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	return out, err
}

const identityCols = `id, tenant_id, customer_id, system, external_customer_id, external_service_id, external_company_id, company_name,
	company_nit, display_name, code, service_state, plan, valid_from, valid_to, synced_at, stale`

func scanIdentity(row pgx.Row) (*domain.ExternalIdentity, error) {
	var i domain.ExternalIdentity
	err := row.Scan(&i.ID, &i.TenantID, &i.CustomerID, &i.System, &i.ExternalCustomerID, &i.ExternalServiceID, &i.ExternalCompanyID,
		&i.CompanyName, &i.CompanyNIT, &i.DisplayName, &i.Code, &i.ServiceState, &i.Plan, &i.ValidFrom, &i.ValidTo, &i.SyncedAt, &i.Stale)
	if err != nil {
		return nil, notFound(err)
	}
	return &i, nil
}

func collectIdentities(rows pgx.Rows) ([]domain.ExternalIdentity, error) {
	defer rows.Close()
	var out []domain.ExternalIdentity
	for rows.Next() {
		i, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// IdentityAt implementa app.IntegrationStore: la identidad vigente en at (nil si no hay).
func (s *Store) IdentityAt(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, at time.Time) (*domain.ExternalIdentity, error) {
	var out *domain.ExternalIdentity
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		i, err := scanIdentity(tx.QueryRow(ctx, `SELECT `+identityCols+` FROM devices.customer_external_identity
			WHERE tenant_id = $1 AND customer_id = $2 AND system = 'argosisp' AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
			ORDER BY valid_from DESC LIMIT 1`, t.UUID(), customerID, at))
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		out = i
		return err
	})
	return out, err
}

// IdentityHistory implementa app.IntegrationStore (más reciente primero).
func (s *Store) IdentityHistory(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, limit int) ([]domain.ExternalIdentity, error) {
	var out []domain.ExternalIdentity
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+identityCols+` FROM devices.customer_external_identity
			WHERE tenant_id = $1 AND customer_id = $2 ORDER BY valid_from DESC, id DESC LIMIT $3`, t.UUID(), customerID, limit)
		if err != nil {
			return err
		}
		out, err = collectIdentities(rows)
		return err
	})
	return out, err
}

// CurrentIdentities implementa app.IntegrationStore.
func (s *Store) CurrentIdentities(ctx context.Context, t pgdb.TenantID, customerIDs []uuid.UUID) (map[uuid.UUID]domain.ExternalIdentity, error) {
	out := map[uuid.UUID]domain.ExternalIdentity{}
	if len(customerIDs) == 0 {
		return out, nil
	}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+identityCols+` FROM devices.customer_external_identity
			WHERE tenant_id = $1 AND customer_id = ANY($2) AND system = 'argosisp' AND valid_to IS NULL`, t.UUID(), customerIDs)
		if err != nil {
			return err
		}
		list, err := collectIdentities(rows)
		for _, i := range list {
			out[i.CustomerID] = i
		}
		return err
	})
	return out, err
}

// CustomerSync implementa app.IntegrationStore.
func (s *Store) CustomerSync(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID) (*time.Time, *string, error) {
	var at *time.Time
	var st *string
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT checked_at, status FROM devices.customer_external_sync
			WHERE tenant_id = $1 AND customer_id = $2 AND system = 'argosisp'`, t.UUID(), customerID).Scan(&at, &st)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return at, st, err
}

func upsertSync(ctx context.Context, tx pgx.Tx, t pgdb.TenantID, customerID uuid.UUID, status string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO devices.customer_external_sync (tenant_id, customer_id, system, checked_at, status)
		VALUES ($1, $2, 'argosisp', $3, $4)
		ON CONFLICT (tenant_id, customer_id, system) DO UPDATE SET checked_at = EXCLUDED.checked_at, status = EXCLUDED.status`,
		t.UUID(), customerID, now, status)
	return err
}

// ApplyIdentityPlan implementa app.IntegrationStore: en una transacción
// aplica el plan (cerrar, abrir o refrescar la vigencia), registra la
// resolución y, si alias cambia el cliente, guarda el alias con su evento.
func (s *Store) ApplyIdentityPlan(ctx context.Context, t pgdb.TenantID, customerID uuid.UUID, plan domain.IdentityPlan, status string,
	now time.Time, alias func(c *domain.Customer, current *domain.ExternalIdentity) bool, ev app.CustomerEvents,
) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		c, err := scanCustomer(tx.QueryRow(ctx, `SELECT `+customerCols+` FROM devices.customer c WHERE c.tenant_id = $1 AND c.id = $2 FOR UPDATE`,
			t.UUID(), customerID))
		if err != nil {
			return err
		}
		if plan.Close {
			if _, err := tx.Exec(ctx, `UPDATE devices.customer_external_identity SET valid_to = greatest($3, valid_from)
				WHERE tenant_id = $1 AND customer_id = $2 AND system = 'argosisp' AND valid_to IS NULL`, t.UUID(), customerID, plan.CloseAt); err != nil {
				return fmt.Errorf("devices: close identity: %w", err)
			}
		}
		if r := plan.Refresh; r != nil {
			if _, err := tx.Exec(ctx, `UPDATE devices.customer_external_identity SET external_customer_id = $3, external_service_id = $4,
				display_name = $5, code = $6, service_state = $7, plan = $8, synced_at = $9, stale = false,
				external_company_id = $10, company_name = $11, company_nit = $12
				WHERE tenant_id = $1 AND id = $2`, t.UUID(), r.ID, r.ExternalCustomerID, r.ExternalServiceID, r.DisplayName, r.Code,
				r.ServiceState, r.Plan, r.SyncedAt, r.ExternalCompanyID, r.CompanyName, r.CompanyNIT); err != nil {
				return fmt.Errorf("devices: refresh identity: %w", err)
			}
		}
		if o := plan.Open; o != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO devices.customer_external_identity (id, tenant_id, customer_id, system, external_customer_id,
				external_service_id, display_name, code, service_state, plan, valid_from, synced_at, stale, external_company_id, company_name,
				company_nit)
				VALUES ($1, $2, $3, 'argosisp', $4, $5, $6, $7, $8, $9, $10, $11, false, $12, $13, $14)`,
				o.ID, t.UUID(), customerID, o.ExternalCustomerID, o.ExternalServiceID, o.DisplayName, o.Code, o.ServiceState, o.Plan,
				o.ValidFrom, o.SyncedAt, o.ExternalCompanyID, o.CompanyName, o.CompanyNIT); err != nil {
				return fmt.Errorf("devices: open identity: %w", err)
			}
		}
		if err := upsertSync(ctx, tx, t, customerID, status, now); err != nil {
			return err
		}
		if alias == nil {
			return nil
		}
		cur, err := scanIdentity(tx.QueryRow(ctx, `SELECT `+identityCols+` FROM devices.customer_external_identity
			WHERE tenant_id = $1 AND customer_id = $2 AND system = 'argosisp' AND valid_to IS NULL`, t.UUID(), customerID))
		if errors.Is(err, domain.ErrNotFound) {
			cur, err = nil, nil
		}
		if err != nil {
			return err
		}
		if !alias(c, cur) {
			return nil
		}
		if err := tx.QueryRow(ctx, `UPDATE devices.customer SET alias = $3, alias_source = $4, updated_at = now(), version = version + 1
			WHERE tenant_id = $1 AND id = $2 RETURNING version, updated_at`, t.UUID(), c.ID, c.Alias, c.AliasSource).
			Scan(&c.Version, &c.UpdatedAt); err != nil {
			return fmt.Errorf("devices: external alias: %w", err)
		}
		if ev == nil {
			return nil
		}
		return emit(ctx, tx, ev(c))
	})
}

// MarkIdentitiesStale implementa app.IntegrationStore: ArgosISP no respondió;
// se conserva la identidad vigente marcada stale.
func (s *Store) MarkIdentitiesStale(ctx context.Context, t pgdb.TenantID, customerIDs []uuid.UUID, now time.Time) error {
	if len(customerIDs) == 0 {
		return nil
	}
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE devices.customer_external_identity SET stale = true
			WHERE tenant_id = $1 AND customer_id = ANY($2) AND system = 'argosisp' AND valid_to IS NULL`, t.UUID(), customerIDs); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO devices.customer_external_sync (tenant_id, customer_id, system, checked_at, status)
			SELECT $1, c.id, 'argosisp', $3, 'error' FROM devices.customer c WHERE c.tenant_id = $1 AND c.id = ANY($2)
			ON CONFLICT (tenant_id, customer_id, system) DO UPDATE SET checked_at = EXCLUDED.checked_at, status = 'error'`,
			t.UUID(), customerIDs, now)
		return err
	})
}

// SweepCandidates implementa app.IntegrationStore: clientes activos sin
// resolución o con la última anterior a before (las más viejas primero).
func (s *Store) SweepCandidates(ctx context.Context, t pgdb.TenantID, before time.Time, limit int) ([]domain.Customer, error) {
	var out []domain.Customer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+customerCols+` FROM devices.customer c
			LEFT JOIN devices.customer_external_sync s ON s.tenant_id = c.tenant_id AND s.customer_id = c.id AND s.system = 'argosisp'
			WHERE c.tenant_id = $1 AND c.status = 'active' AND (s.checked_at IS NULL OR s.checked_at < $2)
			ORDER BY s.checked_at NULLS FIRST, c.id LIMIT $3`, t.UUID(), before, limit)
		if err != nil {
			return err
		}
		out, err = collectCustomers(rows)
		return err
	})
	return out, err
}

// ServiceWindows implementa app.IntegrationStore: periodos de los servicios
// de ArgosISP que solapan [from, to), con la clave del cliente.
func (s *Store) ServiceWindows(ctx context.Context, t pgdb.TenantID, serviceIDs []int64, from, to time.Time) ([]app.ServiceWindow, error) {
	var out []app.ServiceWindow
	if len(serviceIDs) == 0 {
		return out, nil
	}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT i.external_service_id, i.customer_id, c.realm_id, c.site_id, c.address, i.valid_from, i.valid_to
			FROM devices.customer_external_identity i JOIN devices.customer c ON c.tenant_id = i.tenant_id AND c.id = i.customer_id
			WHERE i.tenant_id = $1 AND i.system = 'argosisp' AND i.external_service_id = ANY($2)
			  AND i.valid_from < $4 AND (i.valid_to IS NULL OR i.valid_to > $3)
			ORDER BY i.external_service_id, i.valid_from`, t.UUID(), serviceIDs, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w app.ServiceWindow
			var addr netip.Prefix
			if err := rows.Scan(&w.ServiceID, &w.CustomerID, &w.RealmID, &w.SiteID, &addr, &w.From, &w.To); err != nil {
				return err
			}
			w.Address = addr
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// CustomersByExternal implementa app.IntegrationStore: clientes con identidad
// vigente del servicio o del cliente de ArgosISP.
func (s *Store) CustomersByExternal(ctx context.Context, t pgdb.TenantID, serviceID, customerID *int64) ([]domain.Customer, error) {
	var out []domain.Customer
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+customerCols+` FROM devices.customer c
			JOIN devices.customer_external_identity i ON i.tenant_id = c.tenant_id AND i.customer_id = c.id AND i.system = 'argosisp' AND i.valid_to IS NULL
			WHERE c.tenant_id = $1 AND (($2::bigint IS NOT NULL AND i.external_service_id = $2) OR ($3::bigint IS NOT NULL AND i.external_customer_id = $3))
			LIMIT 500`, t.UUID(), serviceID, customerID)
		if err != nil {
			return err
		}
		out, err = collectCustomers(rows)
		return err
	})
	return out, err
}

// SetServiceCompany implementa app.IntegrationStore: un webhook de ArgosISP
// trae la empresa del servicio (empresa_id, contrato 1.2.0 §2.3). Las
// identidades vigentes del servicio con otra empresa toman la nueva; el
// nombre y el NIT quedan en NULL hasta la próxima resolución (no se conocen).
func (s *Store) SetServiceCompany(ctx context.Context, t pgdb.TenantID, serviceID, companyID int64) (int, error) {
	var n int64
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE devices.customer_external_identity SET external_company_id = $3, company_name = NULL, company_nit = NULL
			WHERE tenant_id = $1 AND system = 'argosisp' AND valid_to IS NULL AND external_service_id = $2
			  AND external_company_id IS DISTINCT FROM $3`, t.UUID(), serviceID, companyID)
		n = tag.RowsAffected()
		return err
	})
	return int(n), err
}

// ServiceIdentities implementa app.IntegrationStore: identidades de los
// servicios de ArgosISP vigentes en at (at = nil: las vigentes ahora) con su
// cliente; sites limita al alcance (nil = todo el ISP). Orden: por servicio,
// el cliente visto más recientemente primero.
func (s *Store) ServiceIdentities(ctx context.Context, t pgdb.TenantID, serviceIDs []int64, at *time.Time, sites []uuid.UUID) ([]app.ServiceIdentity, error) {
	var out []app.ServiceIdentity
	if len(serviceIDs) == 0 {
		return out, nil
	}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT i.id, `+customerCols+` FROM devices.customer_external_identity i
			JOIN devices.customer c ON c.tenant_id = i.tenant_id AND c.id = i.customer_id
			WHERE i.tenant_id = $1 AND i.system = 'argosisp' AND i.external_service_id = ANY($2)
			  AND (($3::timestamptz IS NULL AND i.valid_to IS NULL)
			    OR ($3::timestamptz IS NOT NULL AND i.valid_from <= $3 AND (i.valid_to IS NULL OR i.valid_to > $3)))
			  AND ($4::uuid[] IS NULL OR c.site_id = ANY($4))
			ORDER BY i.external_service_id, c.last_seen DESC, c.id`, t.UUID(), serviceIDs, at, sites)
		if err != nil {
			return fmt.Errorf("devices: service identities: %w", err)
		}
		defer rows.Close()
		ids := map[uuid.UUID]int{}
		for rows.Next() {
			var id uuid.UUID
			var c domain.Customer
			var conf, score, sconf *int16
			if err := rows.Scan(&id, &c.ID, &c.TenantID, &c.RealmID, &c.Address, &c.SiteID, &c.ClientPrefixID, &c.Kind, &c.KindSource,
				&c.KindLocked, &conf, &c.KindChangedAt, &c.CommercialUseSuspected, &c.SecurityState, &c.OpenFindings, &c.Alias,
				&c.AliasSource, &c.Notes, &c.Status, &c.InactiveReason, &c.FirstSeen, &c.LastSeen, &c.ResetAt, &c.CreatedAt, &c.UpdatedAt,
				&c.Version, &score, &c.SuggestedKind, &sconf, &c.SuggestedReasons, &c.SuggestedModel, &c.SuggestedAt, &c.DefaultKind); err != nil {
				return err
			}
			c.KindConfidence, c.CommercialScore, c.SuggestedConfidence = intPtr16(conf), intPtr16(score), intPtr16(sconf)
			ids[id] = len(out)
			out = append(out, app.ServiceIdentity{Customer: c})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(out) == 0 {
			return nil
		}
		list := make([]uuid.UUID, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		irows, err := tx.Query(ctx, `SELECT `+identityCols+` FROM devices.customer_external_identity WHERE tenant_id = $1 AND id = ANY($2)`,
			t.UUID(), list)
		if err != nil {
			return err
		}
		idents, err := collectIdentities(irows)
		for _, i := range idents {
			out[ids[i.ID]].Identity = i
		}
		return err
	})
	return out, err
}

// CountIdentities implementa app.IntegrationStore.
func (s *Store) CountIdentities(ctx context.Context, t pgdb.TenantID) (current, stale int, err error) {
	err = s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE stale) FROM devices.customer_external_identity
			WHERE tenant_id = $1 AND system = 'argosisp' AND valid_to IS NULL`, t.UUID()).Scan(&current, &stale)
	})
	return current, stale, err
}

// InsertArgosWebhook implementa app.IntegrationStore (false = duplicado).
func (s *Store) InsertArgosWebhook(ctx context.Context, t pgdb.TenantID, id, event string, payload []byte) (bool, error) {
	var inserted bool
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO devices.argosisp_webhook_inbox (tenant_id, webhook_id, event, payload)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, t.UUID(), id, event, payload)
		inserted = tag.RowsAffected() == 1
		return err
	})
	return inserted, err
}

// PendingArgosWebhooks implementa app.IntegrationStore (multi-tenant declarado: procesador).
func (s *Store) PendingArgosWebhooks(ctx context.Context, limit int) ([]app.ArgosWebhook, error) {
	var out []app.ArgosWebhook
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, webhook_id, coalesce(event, ''), payload, attempts FROM devices.argosisp_webhook_inbox
			WHERE processed_at IS NULL AND attempts < 10 ORDER BY received_at LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w app.ArgosWebhook
			if err := rows.Scan(&w.TenantID, &w.ID, &w.Event, &w.Payload, &w.Attempts); err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// FinishArgosWebhook implementa app.IntegrationStore.
func (s *Store) FinishArgosWebhook(ctx context.Context, t pgdb.TenantID, id string, done bool) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE devices.argosisp_webhook_inbox SET attempts = attempts + 1,
			processed_at = CASE WHEN $3 THEN now() ELSE NULL END WHERE tenant_id = $1 AND webhook_id = $2`, t.UUID(), id, done)
		return err
	})
}

// PurgeArgosWebhooks implementa app.IntegrationStore (multi-tenant declarado).
func (s *Store) PurgeArgosWebhooks(ctx context.Context, before time.Time) (int, error) {
	var n int64
	err := s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM devices.argosisp_webhook_inbox WHERE received_at < $1`, before)
		n = tag.RowsAffected()
		return err
	})
	return int(n), err
}

// CustomersContaining implementa app.IntegrationStore: por dirección, el
// cliente que la contiene visto más recientemente (un realm privado de otro
// nodo puede repetir la IP).
func (s *Store) CustomersContaining(ctx context.Context, t pgdb.TenantID, addrs []netip.Addr, sites []uuid.UUID) (map[netip.Addr]domain.Customer, error) {
	out := map[netip.Addr]domain.Customer{}
	if len(addrs) == 0 {
		return out, nil
	}
	list := make([]string, len(addrs))
	for i, a := range addrs {
		list[i] = a.Unmap().String()
	}
	siteFilter := ""
	args := []any{t.UUID(), list}
	if sites != nil {
		siteFilter = " AND c.site_id = ANY($3)"
		args = append(args, sites)
	}
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.ip, `+customerCols+` FROM unnest($2::inet[]) AS a(ip)
			CROSS JOIN LATERAL (SELECT * FROM devices.customer c0 WHERE c0.tenant_id = $1 AND c0.address >>= a.ip
				ORDER BY c0.last_seen DESC, c0.id LIMIT 1) c0
			JOIN devices.customer c ON c.tenant_id = c0.tenant_id AND c.id = c0.id
			WHERE true`+siteFilter, args...)
		if err != nil {
			return fmt.Errorf("devices: customers containing: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var ip netip.Addr
			var cust domain.Customer
			var conf, score, sconf *int16
			if err := rows.Scan(&ip, &cust.ID, &cust.TenantID, &cust.RealmID, &cust.Address, &cust.SiteID, &cust.ClientPrefixID, &cust.Kind,
				&cust.KindSource, &cust.KindLocked, &conf, &cust.KindChangedAt, &cust.CommercialUseSuspected, &cust.SecurityState,
				&cust.OpenFindings, &cust.Alias, &cust.AliasSource, &cust.Notes, &cust.Status, &cust.InactiveReason, &cust.FirstSeen,
				&cust.LastSeen, &cust.ResetAt, &cust.CreatedAt, &cust.UpdatedAt, &cust.Version, &score, &cust.SuggestedKind, &sconf,
				&cust.SuggestedReasons, &cust.SuggestedModel, &cust.SuggestedAt, &cust.DefaultKind); err != nil {
				return err
			}
			cust.KindConfidence, cust.CommercialScore, cust.SuggestedConfidence = intPtr16(conf), intPtr16(score), intPtr16(sconf)
			out[ip.Unmap()] = cust
		}
		return rows.Err()
	})
	return out, err
}
