package domain

import (
	"time"

	"github.com/google/uuid"
)

// PasswordResetTTL es la vida de un enlace de restablecimiento de contraseña
// (docs/security.md §4.5: 30 min, un uso).
const PasswordResetTTL = 30 * time.Minute

// PasswordReset es un enlace de restablecimiento (solo se guarda el SHA-256
// del token) con el estado de la cuenta a la que pertenece.
type PasswordReset struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	ExpiresAt       time.Time
	UsedAt          *time.Time
	UserStatus      string
	UserEmail       string
	UserDisplayName string
}

// Problem dice por qué el enlace no sirve en now ("" si sirve): USED,
// EXPIRED o INVALID_TOKEN (la cuenta ya no puede usarlo).
func (r *PasswordReset) Problem(now time.Time) string {
	switch {
	case r.UsedAt != nil:
		return "USED"
	case !now.Before(r.ExpiresAt):
		return "EXPIRED"
	case r.UserStatus != UserActive:
		return "INVALID_TOKEN"
	}
	return ""
}
