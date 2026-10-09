// Package notify implementa el canal mínimo de alertas adelantado al I1
// (D13, D17, D21; api.md §2.10 ter, alerts.yaml): canales por ISP de tipo
// email (SMTP de la instalación), telegram y librenms (por su API), secretos
// write-only cifrados, prueba de conexión, envío de prueba, registro de
// entregas y el consumidor alerts-notify que notifica hallazgos abiertos,
// exportadores silenciosos y túneles caídos, sin la IP del cliente por defecto.
package notify

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// Códigos de error del contrato.
const (
	CodeChannelNotFound         = "NOTIFICATION_CHANNEL_NOT_FOUND"
	CodeKindNotAvailable        = "NOTIFICATION_CHANNEL_KIND_NOT_AVAILABLE"
	CodeChannelUnreachable      = "NOTIFICATION_CHANNEL_UNREACHABLE"
	CodeChannelAuthFailed       = "NOTIFICATION_CHANNEL_AUTH_FAILED"
	CodeChannelCredentialsMiss  = "NOTIFICATION_CHANNEL_CREDENTIALS_MISSING"
	KindEmail                   = "email"
	KindTelegram                = "telegram"
	KindLibreNMS                = "librenms"
	defaultThrottleMinutes      = 15
	defaultLibreNMSTimeoutSecs  = 10
	statusUnverified            = "unverified"
	statusOK                    = "ok"
	statusFailing               = "failing"
	deliveryQueued              = "queued"
	deliverySent                = "sent"
	deliveryFailed              = "failed"
	deliveryThrottled           = "throttled"
	maxErrorLen                 = 300
	defaultEmailSubjectPrefix   = "[Horus]"
	defaultLanguage             = "es"
	eventFindingOpened          = "finding_opened"
	eventFindingReopened        = "finding_reopened"
	eventExporterSilent         = "exporter_silent"
	eventExporterRecovered      = "exporter_recovered"
	eventTunnelDown             = "tunnel_down"
	eventTunnelRecovered        = "tunnel_recovered"
	notificationChannelEventSrc = "horus/alerts"
)

// EventTypes son los tipos de evento suscribibles en I1.
var EventTypes = []string{eventFindingOpened, eventFindingReopened, eventExporterSilent, eventExporterRecovered, eventTunnelDown, eventTunnelRecovered}

// Severities en orden creciente.
var Severities = []string{"info", "low", "medium", "high", "critical"}

// SeverityRank devuelve el orden de una severidad (-1 si es desconocida).
func SeverityRank(s string) int { return slices.Index(Severities, s) }

// Subscription es ChannelSubscription.
type Subscription struct {
	EventTypes      []string    `json:"event_types"`
	MinSeverity     *string     `json:"min_severity,omitempty"`
	SiteIDs         []uuid.UUID `json:"site_ids"`
	ThrottleMinutes *int        `json:"throttle_minutes,omitempty"`
}

// Throttle devuelve la ventana de agrupación.
func (s Subscription) Throttle() time.Duration {
	m := defaultThrottleMinutes
	if s.ThrottleMinutes != nil {
		m = *s.ThrottleMinutes
	}
	return time.Duration(m) * time.Minute
}

// EmailConfig es EmailChannelConfig.
type EmailConfig struct {
	Recipients    []string `json:"recipients"`
	SubjectPrefix string   `json:"subject_prefix,omitempty"`
	Language      string   `json:"language,omitempty"`
}

// TelegramConfig es TelegramChannelConfig.
type TelegramConfig struct {
	ChatID     string `json:"chat_id"`
	UsesOwnBot bool   `json:"uses_own_bot"`
	Language   string `json:"language,omitempty"`
}

// LibreNMSConfig es LibreNmsChannelConfig (D17).
type LibreNMSConfig struct {
	BaseURL        string `json:"base_url"`
	Username       string `json:"username"`
	TLSVerify      *bool  `json:"tls_verify,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// Credentials son los secretos write-only de un canal (cifrados en la base).
type Credentials struct {
	TelegramBotToken string `json:"telegram_bot_token,omitempty"`
	Password         string `json:"password,omitempty"`
	APIToken         string `json:"api_token,omitempty"`
}

// Channel es un canal de notificación del ISP.
type Channel struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	Name                string
	Kind                string
	Enabled             bool
	Config              json.RawMessage
	Subscription        Subscription
	IncludePersonalData bool
	Status              string
	SecretCiphertext    []byte
	DEKWrapped          []byte
	KEKID               *string
	LastDeliveryAt      *time.Time
	LastError           *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Version             int
}

// HasCredentials indica si hay secretos guardados.
func (c *Channel) HasCredentials() bool { return len(c.SecretCiphertext) > 0 }

// Delivery es una entrega registrada.
type Delivery struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	ChannelID       uuid.UUID
	ChannelKind     string
	Status          string
	EventType       *string
	SourceEventType *string
	SourceEventID   *uuid.UUID
	ResourceID      *string
	IsTest          bool
	Error           *string
	CreatedAt       time.Time
	SentAt          *time.Time
}

var (
	chatIDRe   = regexp.MustCompile(`^-?[0-9]{1,20}$|^@[A-Za-z0-9_]{5,32}$`)
	botTokenRe = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{30,}$`)
)

type fieldErrs []problem.FieldError

func (f *fieldErrs) add(field, code, msg string) { *f = append(*f, apperr.Field(field, code, msg)) }

func (f fieldErrs) err() error {
	if len(f) == 0 {
		return nil
	}
	return apperr.Validation(f...)
}

func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// normalizeConfig valida la config del kind y la devuelve normalizada.
func normalizeConfig(kind string, raw json.RawMessage, f *fieldErrs) json.RawMessage {
	switch kind {
	case KindEmail:
		var c EmailConfig
		if decodeStrict(raw, &c) != nil {
			f.add("config", "INVALID_VALUE", "EmailChannelConfig: recipients, subject_prefix, language.")
			return nil
		}
		if len(c.Recipients) < 1 || len(c.Recipients) > 20 {
			f.add("config.recipients", "INVALID_VALUE", "Entre 1 y 20 destinatarios.")
		}
		for _, r := range c.Recipients {
			if a, err := mail.ParseAddress(r); err != nil || a.Address != r {
				f.add("config.recipients", "INVALID_FORMAT", "Email no válido: "+r)
			}
		}
		if c.SubjectPrefix == "" {
			c.SubjectPrefix = defaultEmailSubjectPrefix
		}
		if len([]rune(c.SubjectPrefix)) > 40 {
			f.add("config.subject_prefix", "TOO_LONG", "Máximo 40 caracteres.")
		}
		if c.Language == "" {
			c.Language = defaultLanguage
		}
		if c.Language != "es" && c.Language != "en" {
			f.add("config.language", "INVALID_VALUE", "es o en.")
		}
		out, _ := json.Marshal(c)
		return out
	case KindTelegram:
		var c TelegramConfig
		if decodeStrict(raw, &c) != nil {
			f.add("config", "INVALID_VALUE", "TelegramChannelConfig: chat_id, language.")
			return nil
		}
		if !chatIDRe.MatchString(c.ChatID) {
			f.add("config.chat_id", "INVALID_FORMAT", "ID numérico de chat o @canal.")
		}
		if c.Language == "" {
			c.Language = defaultLanguage
		}
		if c.Language != "es" && c.Language != "en" {
			f.add("config.language", "INVALID_VALUE", "es o en.")
		}
		out, _ := json.Marshal(c)
		return out
	case KindLibreNMS:
		var c LibreNMSConfig
		if decodeStrict(raw, &c) != nil {
			f.add("config", "INVALID_VALUE", "LibreNmsChannelConfig: base_url, username, tls_verify, timeout_seconds.")
			return nil
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(c.BaseURL) > 2048 {
			f.add("config.base_url", "INVALID_FORMAT", "URL http(s) sin credenciales.")
		}
		if l := len([]rune(c.Username)); l < 1 || l > 128 {
			f.add("config.username", "INVALID_VALUE", "Usuario de 1 a 128 caracteres.")
		}
		if c.TLSVerify == nil {
			t := true
			c.TLSVerify = &t
		}
		if c.TimeoutSeconds == 0 {
			c.TimeoutSeconds = defaultLibreNMSTimeoutSecs
		}
		if c.TimeoutSeconds < 2 || c.TimeoutSeconds > 30 {
			f.add("config.timeout_seconds", "OUT_OF_RANGE", "Entre 2 y 30.")
		}
		c.BaseURL = strings.TrimRight(c.BaseURL, "/")
		out, _ := json.Marshal(c)
		return out
	}
	return nil
}

func validateSubscription(s *Subscription, f *fieldErrs) {
	if len(s.EventTypes) == 0 {
		f.add("subscription.event_types", "REQUIRED", "Al menos un tipo de evento.")
	}
	for _, e := range s.EventTypes {
		if !slices.Contains(EventTypes, e) {
			f.add("subscription.event_types", "INVALID_VALUE", fmt.Sprintf("Tipo de evento no permitido: %s.", e))
		}
	}
	if s.MinSeverity != nil && SeverityRank(*s.MinSeverity) < 0 {
		f.add("subscription.min_severity", "INVALID_VALUE", "info, low, medium, high o critical.")
	}
	if s.ThrottleMinutes != nil && (*s.ThrottleMinutes < 0 || *s.ThrottleMinutes > 1440) {
		f.add("subscription.throttle_minutes", "OUT_OF_RANGE", "Entre 0 y 1440.")
	}
	if s.SiteIDs == nil {
		s.SiteIDs = []uuid.UUID{}
	}
}

func truncate(s string) *string {
	if len(s) > maxErrorLen {
		s = s[:maxErrorLen]
	}
	return &s
}
