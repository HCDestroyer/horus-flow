package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// Schema es el esquema PostgreSQL del módulo.
const Schema = "alerts"

var (
	errNotFound = errors.New("alerts: not found")
	errVersion  = errors.New("alerts: version changed")
)

// Store es el repositorio del módulo.
type Store struct{ db *pgdb.DB }

// NewStore crea el repositorio.
func NewStore(db *pgdb.DB) *Store { return &Store{db: db} }

const channelCols = `id, tenant_id, name, kind, enabled, config, subscription, include_personal_data, status, secret_ciphertext,
	dek_wrapped, kek_id, last_delivery_at, last_error, created_at, updated_at, version`

func scanChannel(row pgx.Row) (*Channel, error) {
	var c Channel
	var sub []byte
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Kind, &c.Enabled, &c.Config, &sub, &c.IncludePersonalData, &c.Status,
		&c.SecretCiphertext, &c.DEKWrapped, &c.KEKID, &c.LastDeliveryAt, &c.LastError, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: scan channel: %w", err)
	}
	if err := json.Unmarshal(sub, &c.Subscription); err != nil {
		return nil, fmt.Errorf("alerts: subscription: %w", err)
	}
	return &c, nil
}

func emit(ctx context.Context, tx pgx.Tx, evs []outbox.Event) error {
	for _, ev := range evs {
		if err := outbox.Insert(ctx, tx, Schema, ev); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) listChannels(ctx context.Context, t pgdb.TenantID, after *time.Time, afterID uuid.UUID, limit int, enabledOnly bool) ([]Channel, error) {
	var out []Channel
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := []any{t.UUID(), limit}
		cond := ""
		if enabledOnly {
			cond += " AND enabled"
		}
		if after != nil {
			args = append(args, *after, afterID)
			cond += " AND (created_at, id) > ($3, $4)"
		}
		rows, err := tx.Query(ctx, `SELECT `+channelCols+` FROM alerts.notification_channel WHERE tenant_id = $1`+cond+
			` ORDER BY created_at, id LIMIT $2`, args...)
		if err != nil {
			return fmt.Errorf("alerts: list channels: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanChannel(rows)
			if err != nil {
				return err
			}
			out = append(out, *c)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) getChannel(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*Channel, error) {
	var out *Channel
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		out, err = scanChannel(tx.QueryRow(ctx, `SELECT `+channelCols+` FROM alerts.notification_channel WHERE tenant_id = $1 AND id = $2`, t.UUID(), id))
		return err
	})
	return out, err
}

func (s *Store) insertChannel(ctx context.Context, t pgdb.TenantID, c *Channel) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		sub, _ := json.Marshal(c.Subscription)
		_, err := tx.Exec(ctx, `INSERT INTO alerts.notification_channel (id, tenant_id, name, kind, enabled, config, subscription,
			include_personal_data, status, created_at, updated_at, version) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, 1)`,
			c.ID, t.UUID(), c.Name, c.Kind, c.Enabled, c.Config, sub, c.IncludePersonalData, c.Status, c.CreatedAt)
		if err != nil {
			return fmt.Errorf("alerts: insert channel: %w", err)
		}
		return nil
	})
}

// updateChannel guarda todos los campos editables (expect = 0: sin control de versión).
func (s *Store) updateChannel(ctx context.Context, t pgdb.TenantID, c *Channel, expect int) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		sub, _ := json.Marshal(c.Subscription)
		args := []any{t.UUID(), c.ID, c.Name, c.Enabled, c.Config, sub, c.IncludePersonalData, c.Status, c.SecretCiphertext, c.DEKWrapped,
			c.KEKID, c.LastDeliveryAt, c.LastError}
		cond := ""
		if expect > 0 {
			args = append(args, expect)
			cond = " AND version = $14"
		}
		err := tx.QueryRow(ctx, `UPDATE alerts.notification_channel SET name = $3, enabled = $4, config = $5, subscription = $6,
			include_personal_data = $7, status = $8, secret_ciphertext = $9, dek_wrapped = $10, kek_id = $11, last_delivery_at = $12,
			last_error = $13, updated_at = now(), version = version + 1 WHERE tenant_id = $1 AND id = $2`+cond+` RETURNING version, updated_at`,
			args...).Scan(&c.Version, &c.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return errVersion
		}
		if err != nil {
			return fmt.Errorf("alerts: update channel: %w", err)
		}
		return nil
	})
}

func (s *Store) deleteChannel(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM alerts.notification_channel WHERE tenant_id = $1 AND id = $2 AND version = $3`, t.UUID(), id, expect)
		if err != nil {
			return fmt.Errorf("alerts: delete channel: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errVersion
		}
		return nil
	})
}

// recordStatus actualiza el estado operativo del canal sin tocar la versión.
func (s *Store) recordStatus(ctx context.Context, t pgdb.TenantID, id uuid.UUID, status string, lastErr *string, delivered bool) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE alerts.notification_channel SET status = CASE WHEN enabled THEN $3 ELSE status END, last_error = $4,
			last_delivery_at = CASE WHEN $5 THEN now() ELSE last_delivery_at END WHERE tenant_id = $1 AND id = $2`, t.UUID(), id, status, lastErr, delivered)
		if err != nil {
			return fmt.Errorf("alerts: channel status: %w", err)
		}
		return nil
	})
}

// queueDelivery registra una entrega de forma idempotente (canal, evento
// origen). throttled = true si hubo otra del mismo (canal, tipo, recurso)
// en la ventana. Devuelve false si ya existía.
func (s *Store) queueDelivery(ctx context.Context, t pgdb.TenantID, d *Delivery, throttle time.Duration) (bool, error) {
	inserted := false
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if throttle > 0 && d.ResourceID != nil && !d.IsTest {
			var recent bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alerts.notification_delivery WHERE tenant_id = $1 AND channel_id = $2
				AND event_type = $3 AND resource_id = $4 AND status = 'sent' AND created_at > now() - $5::interval)`,
				t.UUID(), d.ChannelID, d.EventType, d.ResourceID, fmt.Sprintf("%d seconds", int(throttle.Seconds()))).Scan(&recent); err != nil {
				return fmt.Errorf("alerts: throttle: %w", err)
			}
			if recent {
				d.Status = deliveryThrottled
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO alerts.notification_delivery (id, tenant_id, channel_id, channel_kind, status, event_type,
			source_event_type, source_event_id, resource_id, is_test, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (channel_id, source_event_id) WHERE source_event_id IS NOT NULL DO NOTHING`,
			d.ID, t.UUID(), d.ChannelID, d.ChannelKind, d.Status, d.EventType, d.SourceEventType, d.SourceEventID, d.ResourceID, d.IsTest, d.CreatedAt)
		if err != nil {
			return fmt.Errorf("alerts: queue delivery: %w", err)
		}
		inserted = tag.RowsAffected() == 1
		return nil
	})
	return inserted, err
}

// finishDelivery marca el resultado y emite notification.sent|failed.
func (s *Store) finishDelivery(ctx context.Context, t pgdb.TenantID, d *Delivery, ev []outbox.Event) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE alerts.notification_delivery SET status = $3, error = $4, sent_at = $5 WHERE tenant_id = $1 AND id = $2`,
			t.UUID(), d.ID, d.Status, d.Error, d.SentAt); err != nil {
			return fmt.Errorf("alerts: finish delivery: %w", err)
		}
		return emit(ctx, tx, ev)
	})
}

// DeliveryQuery filtra GET /notification-deliveries.
type DeliveryQuery struct {
	ChannelID *uuid.UUID
	Status    string
	Kind      string
	Before    *time.Time
	BeforeID  uuid.UUID
	Limit     int
}

func (s *Store) listDeliveries(ctx context.Context, t pgdb.TenantID, q DeliveryQuery) ([]Delivery, error) {
	var out []Delivery
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		args := []any{t.UUID()}
		cond := ""
		add := func(c string, v any) {
			args = append(args, v)
			cond += fmt.Sprintf(" AND "+c, len(args))
		}
		if q.ChannelID != nil {
			add("channel_id = $%d", *q.ChannelID)
		}
		if q.Status != "" {
			add("status = $%d", q.Status)
		}
		if q.Kind != "" {
			add("channel_kind = $%d", q.Kind)
		}
		if q.Before != nil {
			args = append(args, *q.Before, q.BeforeID)
			cond += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT id, tenant_id, channel_id, channel_kind, status, event_type, source_event_type, source_event_id,
			resource_id, is_test, error, created_at, sent_at FROM alerts.notification_delivery WHERE tenant_id = $1%s
			ORDER BY created_at DESC, id DESC LIMIT %d`, cond, q.Limit), args...)
		if err != nil {
			return fmt.Errorf("alerts: list deliveries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var d Delivery
			if err := rows.Scan(&d.ID, &d.TenantID, &d.ChannelID, &d.ChannelKind, &d.Status, &d.EventType, &d.SourceEventType, &d.SourceEventID,
				&d.ResourceID, &d.IsTest, &d.Error, &d.CreatedAt, &d.SentAt); err != nil {
				return fmt.Errorf("alerts: scan delivery: %w", err)
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}
