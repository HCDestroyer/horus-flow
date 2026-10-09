package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Permisos (C7).
const (
	PermRead   = "alerts.read"
	PermManage = "alerts.manage"
)

// DurableNotify es el consumidor del canal mínimo (streams.yaml consumers_i1).
const DurableNotify = "alerts-notify"

// NotifySubjects son los filtros del durable alerts-notify.
var NotifySubjects = []string{"horus.detection.finding.opened.>", "horus.detection.finding.updated.>", "horus.flows.exporter.silent.>",
	"horus.flows.exporter.recovered.>", "horus.wireguard.peer.handshake_stale.>", "horus.wireguard.peer.handshake_recovered.>"}

// Service implementa los casos de uso del canal mínimo de alertas.
type Service struct {
	st      *Store
	sealer  *envelope.Sealer
	senders *Senders
	cursor  *pagination.Codec
	audit   func() (authapi.AuditRecorder, bool)
	baseURL string
	now     func() time.Time
	logger  *slog.Logger
	// async ejecuta los envíos de prueba (en tests, síncrono).
	async func(func())
}

// NewService crea el servicio.
func NewService(st *Store, sealer *envelope.Sealer, senders *Senders, cursor *pagination.Codec,
	audit func() (authapi.AuditRecorder, bool), baseURL string, logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &Service{st: st, sealer: sealer, senders: senders, cursor: cursor, audit: audit, baseURL: strings.TrimRight(baseURL, "/"),
		now: time.Now, logger: logger, async: func(f func()) { go f() }}
}

func (s *Service) ts() time.Time { return s.now().UTC() }

func scope(ctx context.Context, perm string) (*authz.Principal, pgdb.TenantID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil || p.Type == authz.TypeKiosk {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return nil, pgdb.TenantID{}, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, pgdb.TenantID(tid), nil
}

func (s *Service) record(ctx context.Context, t pgdb.TenantID, action string, c *Channel, changes map[string]any) {
	p := authz.FromContext(ctx)
	rec, ok := s.audit()
	if !ok || p == nil {
		s.logger.WarnContext(ctx, "alerts audit not recorded: auth not local", slog.String("action", action))
		return
	}
	if changes == nil {
		changes = map[string]any{}
	}
	if err := rec.Record(ctx, authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: s.ts(), ActorType: p.Type, ActorID: p.Subject,
		ViaPlatform: p.ViaPlatform, Action: action, ResourceType: "notification_channel", ResourceID: c.ID.String(), Scope: "tenant",
		Outcome: "success", Changes: changes}); err != nil {
		s.logger.ErrorContext(ctx, "alerts audit failed", slog.Any("error", err))
	}
}

// ---------------------------------------------------------------- canales

// ChannelInput es NotificationChannelInput / Patch.
type ChannelInput struct {
	Name                *string          `json:"name"`
	Kind                *string          `json:"kind"`
	Enabled             *bool            `json:"enabled"`
	Config              *json.RawMessage `json:"config"`
	Subscription        *Subscription    `json:"subscription"`
	IncludePersonalData *bool            `json:"include_personal_data"`
}

func (s *Service) apply(c *Channel, in ChannelInput, create bool) error {
	var f fieldErrs
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if l := len([]rune(c.Name)); l == 0 || l > 80 {
		f.add("name", "INVALID_VALUE", "Texto de 1 a 80 caracteres.")
	}
	if create {
		if in.Kind == nil {
			f.add("kind", "REQUIRED", "email, telegram o librenms.")
		} else {
			switch *in.Kind {
			case KindEmail, KindTelegram, KindLibreNMS:
				c.Kind = *in.Kind
			default:
				return &apperr.Error{Kind: apperr.KindInvalid, Code: CodeKindNotAvailable, Detail: "Tipo de canal no disponible: " + *in.Kind}
			}
		}
		if in.Config == nil {
			f.add("config", "REQUIRED", "Obligatorio.")
		}
		if in.Subscription == nil {
			f.add("subscription", "REQUIRED", "Obligatorio.")
		}
	} else if in.Kind != nil && *in.Kind != c.Kind {
		f.add("kind", "READ_ONLY", "El tipo de un canal no cambia.")
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Config != nil && c.Kind != "" {
		if cfg := normalizeConfig(c.Kind, *in.Config, &f); cfg != nil {
			if c.Kind == KindTelegram {
				var tc TelegramConfig
				_ = json.Unmarshal(cfg, &tc)
				tc.UsesOwnBot = c.HasCredentials()
				cfg, _ = json.Marshal(tc)
			}
			c.Config = cfg
		}
	}
	if in.Subscription != nil {
		c.Subscription = *in.Subscription
		validateSubscription(&c.Subscription, &f)
	}
	if in.IncludePersonalData != nil {
		c.IncludePersonalData = *in.IncludePersonalData
	}
	return f.err()
}

// Create es POST /notification-channels (estado unverified).
func (s *Service) Create(ctx context.Context, in ChannelInput) (*Channel, error) {
	_, t, err := scope(ctx, PermManage)
	if err != nil {
		return nil, err
	}
	now := s.ts()
	c := &Channel{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), Enabled: true, Status: statusUnverified, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := s.apply(c, in, true); err != nil {
		return nil, err
	}
	if err := s.st.insertChannel(ctx, t, c); err != nil {
		return nil, err
	}
	s.record(ctx, t, "alerts.channel.created", c, map[string]any{"kind": c.Kind, "include_personal_data": c.IncludePersonalData})
	return c, nil
}

// List es GET /notification-channels.
func (s *Service) List(ctx context.Context, limit int, cursor string) ([]Channel, pagination.Page, error) {
	_, t, err := scope(ctx, PermRead)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	var after *time.Time
	afterID := uuid.Nil
	if cursor != "" {
		cur, err := s.cursor.Decode(cursor, "created_at", "", t.String())
		if err != nil || len(cur.Keys) != 2 {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, e1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, e2 := uuid.Parse(cur.Keys[1])
		if e1 != nil || e2 != nil {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		after, afterID = &at, id
	}
	rows, err := s.st.listChannels(ctx, t, after, afterID, limit+1, false)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	pg := pagination.Page{Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "created_at", Tenant: t.String()})
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []Channel{}
	}
	return rows, pg, nil
}

func (s *Service) get(ctx context.Context, perm string, id uuid.UUID) (pgdb.TenantID, *Channel, error) {
	_, t, err := scope(ctx, perm)
	if err != nil {
		return t, nil, err
	}
	c, err := s.st.getChannel(ctx, t, id)
	if errors.Is(err, errNotFound) {
		return t, nil, apperr.NotFound(CodeChannelNotFound)
	}
	return t, c, err
}

// Get es GET /notification-channels/{id}.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Channel, error) {
	_, c, err := s.get(ctx, PermRead, id)
	return c, err
}

// Update es PATCH /notification-channels/{id}.
func (s *Service) Update(ctx context.Context, id uuid.UUID, expect int, in ChannelInput) (*Channel, error) {
	t, c, err := s.get(ctx, PermManage, id)
	if err != nil {
		return nil, err
	}
	if c.Version != expect {
		return nil, apperr.PreconditionFailed(c)
	}
	before := c.IncludePersonalData
	if err := s.apply(c, in, false); err != nil {
		return nil, err
	}
	if !c.Enabled {
		c.Status = "disabled"
	} else if c.Status == "disabled" {
		c.Status = statusUnverified
	}
	if err := s.st.updateChannel(ctx, t, c, expect); err != nil {
		if errors.Is(err, errVersion) {
			cur, _ := s.st.getChannel(ctx, t, id)
			return nil, apperr.PreconditionFailed(cur)
		}
		return nil, err
	}
	changes := map[string]any{}
	if before != c.IncludePersonalData {
		changes["include_personal_data"] = c.IncludePersonalData
	}
	s.record(ctx, t, "alerts.channel.updated", c, changes)
	return c, nil
}

// Delete es DELETE /notification-channels/{id} (crypto-shredding: la DEK
// envuelta se borra con la fila).
func (s *Service) Delete(ctx context.Context, id uuid.UUID, expect int) error {
	t, c, err := s.get(ctx, PermManage, id)
	if err != nil {
		return err
	}
	if c.Version != expect {
		return apperr.PreconditionFailed(c)
	}
	if err := s.st.deleteChannel(ctx, t, id, expect); err != nil {
		if errors.Is(err, errVersion) {
			return apperr.PreconditionFailed(c)
		}
		return err
	}
	s.record(ctx, t, "alerts.channel.deleted", c, nil)
	return nil
}

func aad(c *Channel) []byte {
	return []byte("alerts_channel:" + c.TenantID.String() + ":" + c.ID.String())
}

// PutCredentials es PUT /notification-channels/{id}/credentials (write-only).
func (s *Service) PutCredentials(ctx context.Context, id uuid.UUID, cr Credentials) error {
	t, c, err := s.get(ctx, PermManage, id)
	if err != nil {
		return err
	}
	var f fieldErrs
	switch c.Kind {
	case KindEmail:
		return apperr.Validation(apperr.Field("kind", "NOT_APPLICABLE", "Los canales email usan el SMTP de la instalación: no tienen secretos."))
	case KindTelegram:
		if cr.Password != "" || cr.APIToken != "" {
			f.add("credentials", "NOT_APPLICABLE", "Solo telegram_bot_token.")
		}
		if !botTokenRe.MatchString(cr.TelegramBotToken) {
			f.add("telegram_bot_token", "INVALID_FORMAT", "Token de bot no válido.")
		}
	case KindLibreNMS:
		if cr.TelegramBotToken != "" {
			f.add("telegram_bot_token", "NOT_APPLICABLE", "Solo password y api_token.")
		}
		if cr.Password == "" && cr.APIToken == "" {
			f.add("password", "REQUIRED", "Obligatoria salvo que envíe api_token.")
		}
		if len(cr.Password) > 256 {
			f.add("password", "TOO_LONG", "Máximo 256.")
		}
		if cr.APIToken != "" && (len(cr.APIToken) < 16 || len(cr.APIToken) > 256) {
			f.add("api_token", "INVALID_VALUE", "Entre 16 y 256 caracteres.")
		}
	}
	if err := f.err(); err != nil {
		return err
	}
	plain, _ := json.Marshal(cr)
	ct, dek, err := s.sealer.Seal(plain, aad(c))
	if err != nil {
		return fmt.Errorf("alerts: seal: %w", err)
	}
	kek := s.sealer.KEKID()
	c.SecretCiphertext, c.DEKWrapped, c.KEKID = ct, dek, &kek
	c.Status = statusUnverified
	if c.Kind == KindTelegram {
		var tc TelegramConfig
		_ = json.Unmarshal(c.Config, &tc)
		tc.UsesOwnBot = true
		c.Config, _ = json.Marshal(tc)
	}
	if err := s.st.updateChannel(ctx, t, c, 0); err != nil {
		return err
	}
	fields := []string{}
	for k, v := range map[string]string{"telegram_bot_token": cr.TelegramBotToken, "password": cr.Password, "api_token": cr.APIToken} {
		if v != "" {
			fields = append(fields, k)
		}
	}
	slices.Sort(fields)
	s.record(ctx, t, "alerts.channel.credentials.updated", c, map[string]any{"fields": fields})
	return nil
}

func (s *Service) credentials(c *Channel) (Credentials, error) {
	var cr Credentials
	if !c.HasCredentials() {
		return cr, nil
	}
	plain, err := s.sealer.Open(c.SecretCiphertext, c.DEKWrapped, aad(c))
	if err != nil {
		return cr, fmt.Errorf("alerts: open credentials: %w", err)
	}
	if err := json.Unmarshal(plain, &cr); err != nil {
		return cr, fmt.Errorf("alerts: credentials: %w", err)
	}
	return cr, nil
}

// ConnectionTest es POST /notification-channels/{id}/connection-test
// (síncrona, timeout 10 s, sin enviar nada).
func (s *Service) ConnectionTest(ctx context.Context, id uuid.UUID) (*Channel, TestResult, time.Time, error) {
	t, c, err := s.get(ctx, PermManage, id)
	if err != nil {
		return nil, TestResult{}, time.Time{}, err
	}
	cr, err := s.credentials(c)
	if err != nil {
		return nil, TestResult{}, time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	var res TestResult
	switch c.Kind {
	case KindEmail:
		res = s.senders.testEmail(ctx)
	case KindTelegram:
		res = s.senders.testTelegram(ctx, cr)
	case KindLibreNMS:
		var lc LibreNMSConfig
		_ = json.Unmarshal(c.Config, &lc)
		res = s.senders.testLibreNMS(ctx, lc, cr)
	}
	ms := int(time.Since(start).Milliseconds())
	res.LatencyMS = &ms
	status := statusOK
	if !res.OK {
		status = statusFailing
		if c.Status == statusUnverified {
			status = statusUnverified
		}
	}
	_ = s.st.recordStatus(context.WithoutCancel(ctx), t, c.ID, status, res.Error, false)
	if lc := c.Kind == KindLibreNMS; lc {
		var cfg LibreNMSConfig
		_ = json.Unmarshal(c.Config, &cfg)
		if cfg.TLSVerify != nil && !*cfg.TLSVerify {
			s.record(ctx, t, "alerts.channel.connection_test.tls_unverified", c, nil)
		}
	}
	return c, res, s.ts(), nil
}

// ---------------------------------------------------------------- entregas

func eventResource(typ string, data map[string]any) string {
	for _, k := range []string{"id", "router_id", "peer_id"} {
		if v, ok := data[k].(string); ok && v != "" {
			return v
		}
	}
	return typ
}

// mapEvent devuelve el tipo de notificación de un evento de dominio.
func mapEvent(typ string, data map[string]any) string {
	switch typ {
	case "horus.detection.finding.opened":
		if prev, _ := data["previous_finding_id"].(string); prev != "" {
			return eventFindingReopened
		}
		return eventFindingOpened
	case "horus.detection.finding.updated":
		if st, _ := data["state"].(string); st == "open" {
			if prev, _ := data["previous_state"].(string); prev == "resolved" || prev == "false_positive" {
				return eventFindingReopened
			}
		}
	case "horus.flows.exporter.silent":
		return eventExporterSilent
	case "horus.flows.exporter.recovered":
		return eventExporterRecovered
	case "horus.wireguard.peer.handshake_stale":
		return eventTunnelDown
	case "horus.wireguard.peer.handshake_recovered":
		return eventTunnelRecovered
	}
	return ""
}

var severityLabel = map[string]string{"info": "informativa", "low": "baja", "medium": "media", "high": "alta", "critical": "crítica"}

// render construye el mensaje sin la IP del cliente (los eventos de
// detection ya no la llevan, events.md §5.7).
func (s *Service) render(eventType string, data map[string]any) (Message, string) {
	sev, _ := data["severity"].(string)
	str := func(k string) string { v, _ := data[k].(string); return v }
	var m Message
	switch eventType {
	case eventFindingOpened, eventFindingReopened:
		summary := str("kind")
		if sm, ok := data["summary"].(map[string]any); ok {
			if t, _ := sm["text"].(string); t != "" {
				summary = t
			}
		}
		verb := "Hallazgo abierto"
		if eventType == eventFindingReopened {
			verb = "Hallazgo reabierto"
		}
		m.Subject = verb + ": señales compatibles con " + strings.ReplaceAll(str("kind"), "_", " ")
		m.Text = summary + "\nSeveridad: " + severityLabel[sev]
		if c := str("confidence_level"); c != "" {
			m.Text += " · confianza " + c
		}
		if id := str("id"); id != "" && s.baseURL != "" {
			m.Link = s.baseURL + "/security/findings/" + id
		}
	case eventExporterSilent:
		m.Subject, m.Text = "Exportador silencioso", "El router "+str("router_id")+" dejó de enviar flujos."
		sev = "high"
	case eventExporterRecovered:
		m.Subject, m.Text = "Exportador recuperado", "El router "+str("router_id")+" vuelve a enviar flujos."
		sev = "info"
	case eventTunnelDown:
		m.Subject, m.Text = "Túnel WireGuard caído", "Sin handshake del router "+str("router_id")+"."
		sev = "high"
	case eventTunnelRecovered:
		m.Subject, m.Text = "Túnel WireGuard recuperado", "El router "+str("router_id")+" vuelve a tener túnel activo."
		sev = "info"
	}
	return m, sev
}

func matches(c *Channel, eventType, severity string, site *uuid.UUID) bool {
	if !c.Enabled || !slices.Contains(c.Subscription.EventTypes, eventType) {
		return false
	}
	if c.Subscription.MinSeverity != nil && severity != "" && SeverityRank(severity) < SeverityRank(*c.Subscription.MinSeverity) {
		return false
	}
	if len(c.Subscription.SiteIDs) > 0 && (site == nil || !slices.Contains(c.Subscription.SiteIDs, *site)) {
		return false
	}
	return true
}

// HandleEvent es el handler del durable alerts-notify: una entrega por canal
// suscrito, idempotente por (canal, evento) e inbox en la misma tabla.
func (s *Service) HandleEvent(ctx context.Context, msg *natsx.Message) error {
	env := msg.Envelope
	if env == nil || msg.Tenant == uuid.Nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return natsx.Permanent(fmt.Errorf("alerts: data: %w", err))
	}
	eventType := mapEvent(env.Type, data)
	if eventType == "" {
		return nil
	}
	t := pgdb.TenantID(msg.Tenant)
	channels, err := s.st.listChannels(ctx, t, nil, uuid.Nil, 1000, true)
	if err != nil {
		return err
	}
	m, sev := s.render(eventType, data)
	var site *uuid.UUID
	if v, ok := data["site_id"].(string); ok {
		if id, err := uuid.Parse(v); err == nil {
			site = &id
		}
	}
	resource := eventResource(env.Type, data)
	var errs []error
	for i := range channels {
		c := &channels[i]
		if !matches(c, eventType, sev, site) {
			continue
		}
		evID := env.ID
		srcType := env.Type
		d := &Delivery{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), ChannelID: c.ID, ChannelKind: c.Kind, Status: deliveryQueued,
			EventType: &eventType, SourceEventType: &srcType, SourceEventID: &evID, ResourceID: &resource, CreatedAt: s.ts()}
		inserted, err := s.st.queueDelivery(ctx, t, d, c.Subscription.Throttle())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !inserted || d.Status == deliveryThrottled {
			continue
		}
		s.deliver(ctx, t, c, d, m, sev)
	}
	return errors.Join(errs...)
}

// deliver envía y registra el resultado (notification.sent|failed).
func (s *Service) deliver(ctx context.Context, t pgdb.TenantID, c *Channel, d *Delivery, m Message, severity string) {
	cr, err := s.credentials(c)
	if err == nil {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		switch c.Kind {
		case KindEmail:
			var ec EmailConfig
			_ = json.Unmarshal(c.Config, &ec)
			err = s.senders.sendEmail(sctx, ec, m)
		case KindTelegram:
			var tc TelegramConfig
			_ = json.Unmarshal(c.Config, &tc)
			err = s.senders.sendTelegram(sctx, tc, cr, m)
		case KindLibreNMS:
			var lc LibreNMSConfig
			_ = json.Unmarshal(c.Config, &lc)
			et := ""
			if d.EventType != nil {
				et = *d.EventType
			}
			err = s.senders.sendLibreNMS(sctx, lc, cr, m, et, severity)
		}
		cancel()
	}
	now := s.ts()
	typ := "horus.alerts.notification.sent"
	d.Status, d.SentAt = deliverySent, &now
	status := statusOK
	if err != nil {
		typ, d.Status, d.SentAt, d.Error = "horus.alerts.notification.failed", deliveryFailed, nil, truncate(err.Error())
		status = statusFailing
		s.logger.WarnContext(ctx, "notification failed", slog.String("channel_id", c.ID.String()), slog.String("kind", c.Kind))
	}
	tid := t.UUID()
	ev := outbox.Event{Type: typ, Source: notificationChannelEventSrc, TenantID: &tid, AggregateType: "notification", AggregateID: d.ID,
		AggregateVersion: 1, Actor: outbox.Actor{Type: "service", ID: "svc:alerts"}, OccurredAt: now, Data: map[string]any{
			"id": d.ID, "alert_id": nil, "channel_id": c.ID, "channel": c.Kind, "recipient_user_id": nil, "event_type": d.EventType,
			"source_event_type": d.SourceEventType, "source_event_id": d.SourceEventID, "is_test": d.IsTest, "error": d.Error,
			"occurred_at": now.Format("2006-01-02T15:04:05.000Z"),
		}}
	bg := context.WithoutCancel(ctx)
	if err := s.st.finishDelivery(bg, t, d, []outbox.Event{ev}); err != nil {
		s.logger.ErrorContext(ctx, "delivery not recorded", slog.Any("error", err))
	}
	_ = s.st.recordStatus(bg, t, c.ID, status, d.Error, err == nil)
}

// Test es POST /notification-channels/{id}/test (202; envío asíncrono).
func (s *Service) Test(ctx context.Context, id uuid.UUID) (*Delivery, error) {
	t, c, err := s.get(ctx, PermManage, id)
	if err != nil {
		return nil, err
	}
	d := &Delivery{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), ChannelID: c.ID, ChannelKind: c.Kind, Status: deliveryQueued,
		IsTest: true, CreatedAt: s.ts()}
	if _, err := s.st.queueDelivery(ctx, t, d, 0); err != nil {
		return nil, err
	}
	m := Message{Subject: "Notificación de prueba", Text: "Este canal de Horus Flow está bien configurado."}
	if s.baseURL != "" {
		m.Link = s.baseURL
	}
	bg := context.WithoutCancel(ctx)
	cp := *d
	s.async(func() { s.deliver(bg, t, c, &cp, m, "info") })
	return d, nil
}

// Deliveries es GET /notification-deliveries.
func (s *Service) Deliveries(ctx context.Context, q DeliveryQuery, cursor string) ([]Delivery, pagination.Page, error) {
	_, t, err := scope(ctx, PermRead)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	if q.Status != "" && !slices.Contains([]string{deliverySent, deliveryFailed, deliveryThrottled}, q.Status) {
		return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "status: sent, failed o throttled")
	}
	if q.Kind != "" && !slices.Contains([]string{KindEmail, KindTelegram, KindLibreNMS}, q.Kind) {
		return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidFilter, "channel_kind")
	}
	fk := pagination.FilterKey(q.Status, q.Kind, fmt.Sprint(q.ChannelID))
	if cursor != "" {
		cur, err := s.cursor.Decode(cursor, "-created_at", fk, t.String())
		if err != nil || len(cur.Keys) != 2 {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		at, e1 := time.Parse(time.RFC3339Nano, cur.Keys[0])
		id, e2 := uuid.Parse(cur.Keys[1])
		if e1 != nil || e2 != nil {
			return nil, pagination.Page{}, apperr.New(apperr.KindBadRequest, problem.CodeInvalidCursor, "")
		}
		q.Before, q.BeforeID = &at, id
	}
	limit := q.Limit
	q.Limit++
	rows, err := s.st.listDeliveries(ctx, t, q)
	if err != nil {
		return nil, pagination.Page{}, err
	}
	pg := pagination.Page{Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next := s.cursor.Encode(pagination.Cursor{Keys: []string{last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID.String()},
			Sort: "-created_at", Filter: fk, Tenant: t.String()})
		pg.NextCursor, pg.HasMore = &next, true
	}
	if rows == nil {
		rows = []Delivery{}
	}
	return rows, pg, nil
}
