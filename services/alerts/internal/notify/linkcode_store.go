package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// LinkCode es un código de vinculación de Telegram (bot de la instalación).
type LinkCode struct {
	Code      string
	TenantID  uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
	Chat      *ChatCandidate
}

func (s *Store) insertLinkCode(ctx context.Context, t pgdb.TenantID, lc *LinkCode) error {
	return s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		// Limpieza perezosa de los caducados del tenant.
		if _, err := tx.Exec(ctx, `DELETE FROM alerts.telegram_link_code WHERE tenant_id = $1 AND expires_at < now() - interval '1 day'`,
			t.UUID()); err != nil {
			return fmt.Errorf("alerts: purge link codes: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO alerts.telegram_link_code (code, tenant_id, created_at, expires_at) VALUES ($1, $2, $3, $4)`,
			lc.Code, t.UUID(), lc.CreatedAt, lc.ExpiresAt); err != nil {
			return fmt.Errorf("alerts: insert link code: %w", err)
		}
		return nil
	})
}

// getLinkCode devuelve un código vigente del tenant (errNotFound si no
// existe, es de otro ISP o caducó).
func (s *Store) getLinkCode(ctx context.Context, t pgdb.TenantID, code string) (*LinkCode, error) {
	var out *LinkCode
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		lc := LinkCode{}
		var id, typ, title, user *string
		err := tx.QueryRow(ctx, `SELECT code, tenant_id, created_at, expires_at, chat_id, chat_type, chat_title, chat_username
			FROM alerts.telegram_link_code WHERE tenant_id = $1 AND code = $2 AND expires_at > now()`, t.UUID(), code).
			Scan(&lc.Code, &lc.TenantID, &lc.CreatedAt, &lc.ExpiresAt, &id, &typ, &title, &user)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("alerts: link code: %w", err)
		}
		if id != nil {
			c := ChatCandidate{ID: *id}
			if typ != nil {
				c.Type = *typ
			}
			if title != nil {
				c.Title = *title
			}
			if user != nil {
				c.Username = *user
			}
			lc.Chat = &c
		}
		out = &lc
		return nil
	})
	return out, err
}

// matchLinkCodes guarda el chat en los códigos vigentes que aparecen en las
// actualizaciones del bot compartido, sean del ISP que sean (rol de
// plataforma: quien lee las actualizaciones las reparte por código; cada ISP
// solo ve después el chat de su propio código).
func (s *Store) matchLinkCodes(ctx context.Context, found map[string]ChatCandidate) error {
	if len(found) == 0 {
		return nil
	}
	return s.db.PlatformTx(ctx, func(tx pgx.Tx) error {
		for code, c := range found {
			if _, err := tx.Exec(ctx, `UPDATE alerts.telegram_link_code SET chat_id = $2, chat_type = $3, chat_title = $4,
				chat_username = nullif($5, ''), matched_at = now() WHERE code = $1 AND expires_at > now()`,
				code, c.ID, c.Type, c.Title, c.Username); err != nil {
				return fmt.Errorf("alerts: match link code: %w", err)
			}
		}
		return nil
	})
}
