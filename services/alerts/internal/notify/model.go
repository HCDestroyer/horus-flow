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
	eventRouterOffline          = "router_offline"
	eventRouterRecovered        = "router_recovered"
	eventRouterCPUHigh          = "router_cpu_high"
	eventRouterMemoryHigh       = "router_memory_high"
	eventRouterTemperatureHigh  = "router_temperature_high"
	eventRouterHardwareFailed   = "router_hardware_failed"
	eventRouterInterfaceDown    = "router_interface_down"
	eventRouterInterfaceUp      = "router_interface_up"
	eventRouterRebooted         = "router_rebooted"
	eventServiceDown            = "service_down"
	eventServiceDegraded        = "service_degraded"
	eventServiceRecovered       = "service_recovered"
	notificationChannelEventSrc = "horus/alerts"
)

// Categorías de eventos: agrupan los tipos para suscribirse por bloque. Una
// suscripción a una categoría incluye también los tipos que se añadan a ella
// más adelante.
const (
	CategorySecurity       = "security"
	CategoryRouters        = "routers"
	CategoryInfrastructure = "infrastructure"
	CategoryExporterTunnel = "exporter_tunnel"
)

// Categories en el orden en que las muestra la consola.
var Categories = []string{CategorySecurity, CategoryRouters, CategoryInfrastructure, CategoryExporterTunnel}

// EventCategory asigna cada tipo de evento a su categoría.
var EventCategory = map[string]string{
	eventFindingOpened: CategorySecurity, eventFindingReopened: CategorySecurity,
	eventRouterOffline: CategoryRouters, eventRouterRecovered: CategoryRouters, eventRouterCPUHigh: CategoryRouters,
	eventRouterMemoryHigh: CategoryRouters, eventRouterTemperatureHigh: CategoryRouters, eventRouterHardwareFailed: CategoryRouters,
	eventRouterInterfaceDown: CategoryRouters, eventRouterInterfaceUp: CategoryRouters, eventRouterRebooted: CategoryRouters,
	eventServiceDown: CategoryInfrastructure, eventServiceDegraded: CategoryInfrastructure, eventServiceRecovered: CategoryInfrastructure,
	eventExporterSilent: CategoryExporterTunnel, eventExporterRecovered: CategoryExporterTunnel,
	eventTunnelDown: CategoryExporterTunnel, eventTunnelRecovered: CategoryExporterTunnel,
}

// EventTypes son los tipos de evento suscribibles.
var EventTypes = []string{eventFindingOpened, eventFindingReopened, eventExporterSilent, eventExporterRecovered, eventTunnelDown, eventTunnelRecovered,
	eventRouterOffline, eventRouterRecovered, eventRouterCPUHigh, eventRouterMemoryHigh, eventRouterTemperatureHigh, eventRouterHardwareFailed,
	eventRouterInterfaceDown, eventRouterInterfaceUp, eventRouterRebooted, eventServiceDown, eventServiceDegraded, eventServiceRecovered}

// RecoveryOf relaciona cada tipo de recuperación con las alarmas que cierra
// (mismo recurso): una recuperación llega siempre al canal que recibió la
// alarma, aunque su severidad (info) quede por debajo de min_severity.
var RecoveryOf = map[string][]string{
	eventRouterRecovered: {eventRouterOffline, eventRouterCPUHigh, eventRouterMemoryHigh, eventRouterTemperatureHigh, eventRouterHardwareFailed},
	eventRouterInterfaceUp: {eventRouterInterfaceDown},
	eventServiceRecovered:  {eventServiceDown, eventServiceDegraded},
	eventExporterRecovered: {eventExporterSilent},
	eventTunnelRecovered:   {eventTunnelDown},
}

// Severities en orden creciente.
var Severities = []string{"info", "low", "medium", "high", "critical"}

// SeverityRank devuelve el orden de una severidad (-1 si es desconocida).
func SeverityRank(s string) int { return slices.Index(Severities, s) }

// Subscription es ChannelSubscription. Las suscripciones guardadas antes de
// las categorías solo traen event_types y siguen valiendo tal cual.
type Subscription struct {
	EventTypes      []string    `json:"event_types"`
	Categories      []string    `json:"categories,omitempty"`
	MinSeverity     *string     `json:"min_severity,omitempty"`
	SiteIDs         []uuid.UUID `json:"site_ids"`
	ThrottleMinutes *int        `json:"throttle_minutes,omitempty"`
	// NotifyRecovery (true por defecto): las recuperaciones llegan al canal
	// que recibió la alarma, sin filtro de severidad.
	NotifyRecovery *bool `json:"notify_recovery,omitempty"`
}

// Subscribes indica si el canal está suscrito al tipo (directo o por categoría).
func (s Subscription) Subscribes(eventType string) bool {
	if slices.Contains(s.EventTypes, eventType) {
		return true
	}
	cat, ok := EventCategory[eventType]
	return ok && slices.Contains(s.Categories, cat)
}

// Recovery devuelve notify_recovery con su valor por defecto (true).
func (s Subscription) Recovery() bool { return s.NotifyRecovery == nil || *s.NotifyRecovery }

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
	// MessageThreadID: tema de un supergrupo con temas (foro).
	MessageThreadID *int64 `json:"message_thread_id,omitempty"`
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
	// Cola persistente (D23): intentos hechos, mensaje renderizado (sin
	// datos personales), severidad y traza del evento de origen.
	Attempts    int
	Message     *Message
	Severity    *string
	TraceParent *string
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
			f.add("config", "INVALID_VALUE", "TelegramChannelConfig: chat_id, language, message_thread_id.")
			return nil
		}
		c.ChatID = strings.TrimSpace(c.ChatID)
		if !chatIDRe.MatchString(c.ChatID) {
			f.add("config.chat_id", "INVALID_FORMAT", "ID numérico de chat (los grupos y canales empiezan por -100) o @canal.")
		}
		if c.MessageThreadID != nil && *c.MessageThreadID <= 0 {
			f.add("config.message_thread_id", "OUT_OF_RANGE", "ID de tema positivo.")
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
	if s.EventTypes == nil {
		s.EventTypes = []string{}
	}
	if len(s.EventTypes) == 0 && len(s.Categories) == 0 {
		f.add("subscription.event_types", "REQUIRED", "Al menos una categoría o un tipo de evento.")
	}
	for _, e := range s.EventTypes {
		if !slices.Contains(EventTypes, e) {
			f.add("subscription.event_types", "INVALID_VALUE", fmt.Sprintf("Tipo de evento no permitido: %s.", e))
		}
	}
	for _, c := range s.Categories {
		if !slices.Contains(Categories, c) {
			f.add("subscription.categories", "INVALID_VALUE", fmt.Sprintf("Categoría no permitida: %s.", c))
		}
	}
	s.EventTypes = slices.Compact(slices.Sorted(slices.Values(s.EventTypes)))
	s.Categories = slices.Compact(slices.Sorted(slices.Values(s.Categories)))
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
