package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Enlace de conexión con ArgosISP (contrato docs/integrations/argosisp.md §1.4, H10).
const (
	PermAPITokensManage        = "api_tokens.manage"
	PermAlertsManage           = "alerts.manage"
	CodeConnectionLinkInvalid  = "CONNECTION_LINK_INVALID"
	CodeConnectionLinkExpired  = "CONNECTION_LINK_EXPIRED"
	CodeConnectionLinkRejected = "CONNECTION_LINK_REJECTED"
	argosTemplate              = "argosisp"
	linkTokenName              = "ArgosISP (enlace de conexión)"
	linkRetention              = 7 * 24 * time.Hour
	peerSystemArgos            = "argosisp"
	selfSystem                 = "horus"
)

// argosTemplatePermissions son los permisos de la plantilla ArgosISP de auth
// (§1.1). Solo sirven para avisar antes de generar el enlace; auth vuelve a
// comprobarlos al emitir el token en el canje.
var argosTemplatePermissions = []string{"customers.read", "security.findings.read", "snmp.read", "sso.issue", "traffic.customer.read"}

// ArgosPeer habla con ArgosISP fuera del resolvedor (canje del enlace y
// tickets de autologin). Las salidas pasan por la guardia de egreso.
type ArgosPeer interface {
	// RedeemLink hace POST <apiOrigin>/api/v1/integraciones/horus/conexion/canjear (sin token).
	RedeemLink(ctx context.Context, apiOrigin string, body []byte) (int, []byte, error)
	// RequestSSOTicket hace POST <baseURL>/integraciones/horus/sso/tickets con el token atk_.
	RequestSSOTicket(ctx context.Context, tenant uuid.UUID, baseURL, token string, body []byte) (int, []byte, error)
	// CheckURL comprueba un destino con la guardia de egreso (https y dirección pública).
	CheckURL(ctx context.Context, raw string) error
}

// ConnectionLinkStore guarda los enlaces de conexión (solo el hash del código).
type ConnectionLinkStore interface {
	CreateConnectionLink(ctx context.Context, t pgdb.TenantID, l *domain.ConnectionLink) error
	// ClaimConnectionLink marca usado el enlace vigente (o devuelve la
	// repetición de la misma petición); domain.ErrNotFound → 410.
	ClaimConnectionLink(ctx context.Context, hash, requestHash []byte, now time.Time, ip string) (*domain.ConnectionLink, bool, error)
	CompleteConnectionLink(ctx context.Context, t pgdb.TenantID, id uuid.UUID, ct, dek []byte, kekID string) error
	ReleaseConnectionLink(ctx context.Context, t pgdb.TenantID, id uuid.UUID) error
	PurgeConnectionLinks(ctx context.Context, before time.Time) (int, error)
}

// Ecosystem son las piezas de otros módulos que usan el enlace y el autologin
// (module.Services; false = no disponible en el proceso).
type Ecosystem struct {
	Links    ConnectionLinkStore
	Peer     ArgosPeer
	Tokens   func() (authapi.IntegrationTokens, bool)
	Webhooks func() (alertsapi.IntegrationWebhooks, bool)
	Tickets  func() (authapi.SSOTickets, bool)
	Profiles func() (authapi.UserProfiles, bool)
	Router   func(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
}

func (e *Ecosystem) defaults() {
	if e.Tokens == nil {
		e.Tokens = func() (authapi.IntegrationTokens, bool) { return nil, false }
	}
	if e.Webhooks == nil {
		e.Webhooks = func() (alertsapi.IntegrationWebhooks, bool) { return nil, false }
	}
	if e.Tickets == nil {
		e.Tickets = func() (authapi.SSOTickets, bool) { return nil, false }
	}
	if e.Profiles == nil {
		e.Profiles = func() (authapi.UserProfiles, bool) { return nil, false }
	}
}

func errUnavailable(what string) error {
	return apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, what+" no está disponible en este proceso.")
}

// recordAs audita con un actor explícito (canje público, sin principal).
func (s *Integrations) recordAs(ctx context.Context, t pgdb.TenantID, actorType, actorID, action, outcome, ip string, changes map[string]any) {
	rec, ok := s.audit()
	if !ok {
		s.logger.WarnContext(ctx, "integration audit not recorded: auth not local", slog.String("action", action))
		return
	}
	if changes == nil {
		changes = map[string]any{}
	}
	if err := rec.Record(ctx, authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: s.ts(), ActorType: actorType, ActorID: actorID, Action: action,
		ResourceType: "integration", Scope: "tenant", Outcome: outcome, IP: ip, Changes: changes}); err != nil {
		s.logger.ErrorContext(ctx, "integration audit failed", slog.String("action", action), slog.Any("error", err))
	}
}

// linkScope exige el permiso de configurar la integración y, como el enlace
// emite un API token y crea un canal webhook, también api_tokens.manage y
// alerts.manage; solo una persona miembro del ISP (el token sale a su nombre).
func linkScope(ctx context.Context) (*authz.Principal, pgdb.TenantID, error) {
	p, t, err := scope(ctx, PermSettingsManage)
	if err != nil {
		return nil, t, err
	}
	if !p.Has(PermAPITokensManage) || !p.Has(PermAlertsManage) {
		return nil, t, apperr.Forbidden(problem.CodePermissionDenied, "El enlace de conexión exige además api_tokens.manage y alerts.manage.")
	}
	if p.Type != authz.TypeUser || p.UserID == uuid.Nil || p.ViaPlatform {
		return nil, t, apperr.Forbidden(problem.CodePermissionDenied, "Solo un miembro del ISP puede conectar ArgosISP (el código sale a su nombre).")
	}
	for _, perm := range argosTemplatePermissions {
		if !p.Has(perm) {
			return nil, t, apperr.Forbidden(problem.CodePermissionDenied, "El código de conexión lleva el permiso "+perm+", que usted no tiene.")
		}
	}
	return p, t, nil
}

// ConnectionLinkIssued es el enlace generado (se muestra una vez).
type ConnectionLinkIssued struct {
	URL       string
	ExpiresAt time.Time
}

// CreateConnectionLink es POST /integrations/argosisp/connection-links.
func (s *Integrations) CreateConnectionLink(ctx context.Context) (*ConnectionLinkIssued, error) {
	p, t, err := linkScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.baseURL == "" {
		return nil, apperr.Conflict(CodeIntegrationNotConfig, "Configure HORUS_PUBLIC_BASE_URL: el enlace y el receptor de webhooks la necesitan.")
	}
	if s.eco.Links == nil {
		return nil, errUnavailable("El enlace de conexión")
	}
	code, err := domain.GenerateConnectionCode()
	if err != nil {
		return nil, err
	}
	now := s.ts()
	l := &domain.ConnectionLink{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), CodeHash: domain.HashConnectionCode(code), CreatedBy: p.UserID,
		CreatedAt: now, ExpiresAt: now.Add(domain.ConnectionLinkTTL)}
	if err := s.eco.Links.CreateConnectionLink(ctx, t, l); err != nil {
		return nil, err
	}
	s.record(ctx, t, "integrations.argosisp.connection_link.created", "integration", l.ID.String(),
		map[string]any{"expires_at": l.ExpiresAt.Format(time.RFC3339)})
	return &ConnectionLinkIssued{URL: s.baseURL + domain.ConnectPath + "#" + code, ExpiresAt: l.ExpiresAt}, nil
}

// RedeemCodeInput es el cuerpo del canje (§1.4, claves del contrato).
type RedeemCodeInput struct {
	Codigo  string `json:"codigo"`
	Sistema string `json:"sistema"`
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
	Webhook *struct {
		URL     string `json:"url"`
		Secreto string `json:"secreto"`
	} `json:"webhook"`
}

type redeemResponse struct {
	Token   string `json:"token"`
	Webhook struct {
		URL     string `json:"url"`
		Secreto string `json:"secreto"`
	} `json:"webhook"`
	ConectorID string `json:"conector_id"`
}

// printable rechaza espacios y caracteres de control (tokens y secretos).
func printable(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validWebhookURL valida la URL de un receptor (https, sin credenciales).
func validWebhookURL(raw string, allowHTTP bool) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Host != "" && u.User == nil && u.Fragment == "" &&
		(u.Scheme == "https" || (allowHTTP && u.Scheme == "http"))
}

func (in *RedeemCodeInput) validate(allowHTTP bool) (string, error) {
	var fe fieldErrs
	if !domain.LooksLikeConnectionCode(in.Codigo) {
		fe.add("codigo", "INVALID_FORMAT", "Código del enlace (43 caracteres base64url).")
	}
	if in.Sistema != peerSystemArgos {
		fe.add("sistema", "INVALID_VALUE", "Este enlace solo se canjea desde ArgosISP (sistema = argosisp).")
	}
	origin, ok := domain.ParseOrigin(in.BaseURL, allowHTTP)
	if !ok {
		fe.add("base_url", "INVALID_FORMAT", "Origen https de ArgosISP, sin credenciales ni ruta.")
	}
	if n := len(in.Token); n < 8 || n > 512 || !printable(in.Token) {
		fe.add("token", "INVALID_VALUE", "Token atk_… de ArgosISP (8–512 caracteres).")
	}
	if in.Webhook == nil {
		fe.add("webhook", "REQUIRED", "URL y secreto del receptor de ArgosISP.")
	} else {
		if !validWebhookURL(in.Webhook.URL, allowHTTP) {
			fe.add("webhook.url", "INVALID_FORMAT", "URL https del receptor, sin credenciales.")
		}
		if n := len(in.Webhook.Secreto); n < 16 || n > 256 || !printable(in.Webhook.Secreto) {
			fe.add("webhook.secreto", "INVALID_VALUE", "Secreto de 16 a 256 caracteres.")
		}
	}
	return origin, fe.err()
}

// requestHash identifica la petición para la idempotencia (JSON canónico).
func (in *RedeemCodeInput) requestHash() []byte {
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	return sum[:]
}

func linkAAD(t pgdb.TenantID, id uuid.UUID) []byte {
	return []byte("devices_argosisp:link_response:" + t.String() + ":" + id.String())
}

var errLinkGone = &apperr.Error{Kind: apperr.KindGone, Code: CodeConnectionLinkInvalid,
	Detail: "El enlace de conexión venció, ya se usó o no existe. Genere uno nuevo en Horus."}

// RedeemConnectionCode es POST /integrations/argosisp/conexion/canjear
// (público: el código de un uso es la credencial). Devuelve el cuerpo de la
// respuesta y si es la repetición idempotente de un canje ya hecho.
func (s *Integrations) RedeemConnectionCode(ctx context.Context, in RedeemCodeInput, ip string) ([]byte, bool, error) {
	origin, err := in.validate(s.allowPrivate)
	if err != nil {
		return nil, false, err
	}
	if s.eco.Links == nil || s.eco.Peer == nil || s.baseURL == "" {
		return nil, false, errUnavailable("El enlace de conexión")
	}
	now := s.ts()
	link, replay, err := s.eco.Links.ClaimConnectionLink(ctx, domain.HashConnectionCode(in.Codigo), in.requestHash(), now, ip)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, errLinkGone
	}
	if err != nil {
		return nil, false, err
	}
	t := pgdb.TenantID(link.TenantID)
	if replay {
		body, err := s.sealer.Open(link.ResponseCiphertext, link.ResponseDEK, linkAAD(t, link.ID))
		if err != nil {
			return nil, false, fmt.Errorf("devices: open link response: %w", err)
		}
		return body, true, nil
	}
	body, err := s.completeGenerated(ctx, t, link, in, origin, ip)
	if err != nil {
		var ae *apperr.Error
		// Un error del enlace en sí (permisos del generador) lo consume; uno
		// interno o de un destino rechazado lo libera mientras no caduque.
		if !errors.As(err, &ae) || ae.Code != CodeConnectionLinkRejected {
			if rerr := s.eco.Links.ReleaseConnectionLink(context.WithoutCancel(ctx), t, link.ID); rerr != nil {
				s.logger.WarnContext(ctx, "connection link release failed", slog.Any("error", rerr))
			}
		}
		s.recordAs(ctx, t, "service", "integration:argosisp", "integrations.argosisp.connection_link.redeemed", "failure", ip,
			map[string]any{"link_id": link.ID.String(), "error": errText(err)})
		return nil, false, err
	}
	return body, false, nil
}

func errText(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return "internal"
}

// completeGenerated hace el canje de un enlace generado en Horus (Horus = A).
func (s *Integrations) completeGenerated(ctx context.Context, t pgdb.TenantID, link *domain.ConnectionLink, in RedeemCodeInput,
	origin, ip string,
) ([]byte, error) {
	for _, u := range []string{origin, in.Webhook.URL} {
		if err := s.eco.Peer.CheckURL(ctx, u); err != nil {
			return nil, apperr.Validation(apperr.Field("base_url", "NOT_ALLOWED", "Destino no permitido: solo https y direcciones públicas."))
		}
	}
	tokens, ok := s.eco.Tokens()
	if !ok {
		return nil, errUnavailable("La emisión de API tokens")
	}
	hooks, ok := s.eco.Webhooks()
	if !ok {
		return nil, errUnavailable("El canal webhook")
	}
	conn, expect, err := s.connectorForLink(ctx, t)
	if err != nil {
		return nil, err
	}
	issued, err := tokens.IssueIntegrationToken(ctx, authapi.IntegrationTokenRequest{TenantID: t.UUID(), UserID: link.CreatedBy,
		Template: argosTemplate, Name: linkTokenName, Via: "connection_link", IP: ip})
	if errors.Is(err, authapi.ErrIntegrationTokenNotGranted) {
		return nil, apperr.New(apperr.KindInvalid, CodeConnectionLinkRejected,
			"Quien generó el enlace ya no puede emitir el código de conexión (permisos o membresía). Genere uno nuevo.")
	}
	if err != nil {
		return nil, err
	}
	revoke := func(reason string) {
		if rerr := tokens.RevokeIntegrationToken(context.WithoutCancel(ctx), t.UUID(), issued.ID, reason); rerr != nil {
			s.logger.WarnContext(ctx, "integration token revoke failed", slog.Any("error", rerr))
		}
	}
	chID, err := hooks.UpsertIntegrationWebhook(ctx, t.UUID(), alertsapi.IntegrationWebhook{ChannelID: conn.WebhookChannelID, Name: "ArgosISP",
		URL: in.Webhook.URL, Secret: in.Webhook.Secreto, ActorID: link.CreatedBy})
	if err != nil {
		revoke("connection_link_failed")
		if errors.Is(err, alertsapi.ErrIntegrationWebhookInvalid) {
			return nil, apperr.Validation(apperr.Field("webhook.url", "INVALID_FORMAT", "URL https del receptor, sin credenciales."))
		}
		return nil, err
	}
	secret, err := s.freshWebhookSecret(conn)
	if err != nil {
		revoke("connection_link_failed")
		return nil, err
	}
	prev, err := s.applyLink(conn, origin, in.Token, domain.LinkGenerated, nil, issued.ID, chID)
	if err != nil {
		revoke("connection_link_failed")
		return nil, err
	}
	if err := s.store.SaveArgosConnector(ctx, t, conn, expect); err != nil {
		revoke("connection_link_failed")
		if errors.Is(err, ErrVersionChanged) {
			return nil, apperr.Conflict(problem.CodeConflict, "El conector cambió mientras se conectaba; vuelva a intentarlo.")
		}
		return nil, err
	}
	s.revokePrevious(ctx, tokens, t, prev, issued.ID)
	var out redeemResponse
	out.Token, out.Webhook.URL, out.Webhook.Secreto, out.ConectorID = issued.Raw, *s.WebhookURL(conn), secret, conn.ID.String()
	body, _ := json.Marshal(out)
	ct, dek, err := s.sealer.Seal(body, linkAAD(t, link.ID))
	if err != nil {
		return nil, fmt.Errorf("devices: seal link response: %w", err)
	}
	if err := s.eco.Links.CompleteConnectionLink(ctx, t, link.ID, ct, dek, s.sealer.KEKID()); err != nil {
		return nil, err
	}
	s.recordAs(ctx, t, "service", "integration:argosisp", "integrations.argosisp.connection_link.redeemed", "success", ip,
		map[string]any{"link_id": link.ID.String(), "method": domain.LinkGenerated, "created_by": link.CreatedBy.String(),
			"peer": hostOf(origin), "api_token_id": issued.ID.String(), "notification_channel_id": chID.String(), "enabled": false})
	return body, nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

// connectorForLink devuelve el conector del ISP (o uno nuevo) y su versión.
func (s *Integrations) connectorForLink(ctx context.Context, t pgdb.TenantID) (*domain.ArgosConnector, int, error) {
	c, err := s.store.GetArgosConnector(ctx, t)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return &domain.ArgosConnector{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID()}, 0, nil
	case err != nil:
		return nil, 0, err
	}
	return c, c.Version, nil
}

// freshWebhookSecret genera el secreto nuevo del receptor de Horus (el
// anterior sigue valiendo 24 h) y lo deja cifrado en c.
func (s *Integrations) freshWebhookSecret(c *domain.ArgosConnector) (string, error) {
	sec, err := webhooksig.NewSecret()
	if err != nil {
		return "", err
	}
	ct, dek, err := s.sealer.Seal([]byte(sec), connAAD(c, "webhook"))
	if err != nil {
		return "", fmt.Errorf("devices: seal webhook secret: %w", err)
	}
	if c.HasSecret() {
		until := s.ts().Add(webhookSecretGrace)
		c.PrevSecretCiphertext, c.PrevSecretDEK, c.PrevSecretUntil = c.SecretCiphertext, c.SecretDEK, &until
	}
	kek := s.sealer.KEKID()
	c.SecretCiphertext, c.SecretDEK, c.KEKID = ct, dek, &kek
	return sec, nil
}

// applyLink deja en c lo recibido en el canje: URL base, token atk_ cifrado
// y conector APAGADO hasta que un administrador lo active. Devuelve el API
// token emitido en un canje anterior (para revocarlo).
func (s *Integrations) applyLink(c *domain.ArgosConnector, origin, token, method string, peerConnector *string, tokenID, channelID uuid.UUID,
) (*uuid.UUID, error) {
	ct, dek, err := s.sealer.Seal([]byte(token), connAAD(c, "token"))
	if err != nil {
		return nil, fmt.Errorf("devices: seal argos token: %w", err)
	}
	kek := s.sealer.KEKID()
	now := s.ts()
	prev := c.PeerAPITokenID
	tid, chid := tokenID, channelID
	c.BaseURL, c.Enabled = origin+"/api/v1", false
	c.TokenCiphertext, c.TokenDEK, c.KEKID = ct, dek, &kek
	c.LinkedAt, c.LinkMethod, c.PeerConnectorID, c.PeerAPITokenID, c.WebhookChannelID = &now, &method, peerConnector, &tid, &chid
	c.Failures, c.BackoffUntil = 0, nil
	return prev, nil
}

func (s *Integrations) revokePrevious(ctx context.Context, tokens authapi.IntegrationTokens, t pgdb.TenantID, prev *uuid.UUID, keep uuid.UUID) {
	if prev == nil || *prev == keep {
		return
	}
	if err := tokens.RevokeIntegrationToken(ctx, t.UUID(), *prev, "connection_link_replaced"); err != nil {
		s.logger.WarnContext(ctx, "previous integration token revoke failed", slog.Any("error", err))
	}
}

// RedeemLink es POST /integrations/argosisp/connection-links/redeem: Horus
// canjea un enlace generado en ArgosISP (Horus = B).
func (s *Integrations) RedeemLink(ctx context.Context, raw string) (*ConnectorView, error) {
	p, t, err := linkScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.baseURL == "" {
		return nil, apperr.Conflict(CodeIntegrationNotConfig, "Configure HORUS_PUBLIC_BASE_URL: ArgosISP la necesita para leer de Horus.")
	}
	link, ok := domain.ParseConnectionLink(raw, s.allowPrivate)
	if !ok {
		return nil, apperr.Validation(apperr.Field("link", "INVALID_FORMAT", "Pegue el enlace completo de ArgosISP: https://…/conectar#…"))
	}
	if s.eco.Peer == nil {
		return nil, errUnavailable("El enlace de conexión")
	}
	if err := s.eco.Peer.CheckURL(ctx, link.APIOrigin); err != nil {
		return nil, apperr.Validation(apperr.Field("link", "NOT_ALLOWED", "Destino no permitido: solo https y direcciones públicas."))
	}
	tokens, ok := s.eco.Tokens()
	if !ok {
		return nil, errUnavailable("La emisión de API tokens")
	}
	hooks, ok := s.eco.Webhooks()
	if !ok {
		return nil, errUnavailable("El canal webhook")
	}
	conn, expect, err := s.connectorForLink(ctx, t)
	if err != nil {
		return nil, err
	}
	issued, err := tokens.IssueIntegrationToken(ctx, authapi.IntegrationTokenRequest{TenantID: t.UUID(), UserID: p.UserID,
		Template: argosTemplate, Name: linkTokenName, Via: "connection_link"})
	if errors.Is(err, authapi.ErrIntegrationTokenNotGranted) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "No tiene todos los permisos del código de conexión ArgosISP.")
	}
	if err != nil {
		return nil, err
	}
	failed := func(e error) (*ConnectorView, error) {
		if rerr := tokens.RevokeIntegrationToken(context.WithoutCancel(ctx), t.UUID(), issued.ID, "connection_link_failed"); rerr != nil {
			s.logger.WarnContext(ctx, "integration token revoke failed", slog.Any("error", rerr))
		}
		s.record(ctx, t, "integrations.argosisp.connection_link.redeemed", "integration", conn.ID.String(),
			map[string]any{"method": domain.LinkRedeemed, "peer": hostOf(link.APIOrigin), "ok": false, "error": errText(e)})
		return nil, e
	}
	secret, err := s.freshWebhookSecret(conn)
	if err != nil {
		return failed(err)
	}
	var req RedeemCodeInput
	req.Codigo, req.Sistema, req.BaseURL, req.Token = link.Code, selfSystem, s.baseURL, issued.Raw
	req.Webhook = &struct {
		URL     string `json:"url"`
		Secreto string `json:"secreto"`
	}{URL: *s.WebhookURL(conn), Secreto: secret}
	body, _ := json.Marshal(req)
	status, resp, err := s.eco.Peer.RedeemLink(ctx, link.APIOrigin, body)
	switch {
	case errors.Is(err, ErrArgosNotAllowed):
		return failed(apperr.Validation(apperr.Field("link", "NOT_ALLOWED", "Destino no permitido: solo https y direcciones públicas.")))
	case err != nil:
		return failed(apperr.New(apperr.KindBadGateway, CodeIntegrationUnavailble, "ArgosISP no respondió al canje del enlace."))
	case status == 410:
		return failed(apperr.New(apperr.KindInvalid, CodeConnectionLinkExpired, "ArgosISP dice que el enlace venció o ya se usó. Genere otro en ArgosISP."))
	case status == 400 || status == 422:
		return failed(apperr.New(apperr.KindInvalid, CodeConnectionLinkRejected, "ArgosISP rechazó los datos del canje."))
	case status != 200 && status != 201:
		return failed(apperr.New(apperr.KindBadGateway, CodeIntegrationUnavailble, "Respuesta inesperada de ArgosISP (HTTP "+strconv.Itoa(status)+")."))
	}
	peer, err := s.parsePeerResponse(ctx, resp)
	if err != nil {
		return failed(err)
	}
	chID, err := hooks.UpsertIntegrationWebhook(ctx, t.UUID(), alertsapi.IntegrationWebhook{ChannelID: conn.WebhookChannelID, Name: "ArgosISP",
		URL: peer.Webhook.URL, Secret: peer.Webhook.Secreto, ActorID: p.UserID})
	if err != nil {
		return failed(err)
	}
	pc := peer.ConectorID
	prev, err := s.applyLink(conn, link.APIOrigin, peer.Token, domain.LinkRedeemed, &pc, issued.ID, chID)
	if err != nil {
		return failed(err)
	}
	if err := s.store.SaveArgosConnector(ctx, t, conn, expect); err != nil {
		if errors.Is(err, ErrVersionChanged) {
			return failed(apperr.Conflict(problem.CodeConflict, "El conector cambió mientras se conectaba; vuelva a intentarlo."))
		}
		return failed(err)
	}
	s.revokePrevious(ctx, tokens, t, prev, issued.ID)
	s.record(ctx, t, "integrations.argosisp.connection_link.redeemed", "integration", conn.ID.String(),
		map[string]any{"method": domain.LinkRedeemed, "peer": hostOf(link.APIOrigin), "ok": true, "api_token_id": issued.ID.String(),
			"notification_channel_id": chID.String(), "enabled": false})
	return s.view(ctx, t)
}

// peerRedeem es la respuesta de ArgosISP al canje (conector_id puede ser
// número o texto: es opaco).
type peerRedeem struct {
	Token   string `json:"token"`
	Webhook struct {
		URL     string `json:"url"`
		Secreto string `json:"secreto"`
	} `json:"webhook"`
	ConectorID string
}

func (s *Integrations) parsePeerResponse(ctx context.Context, raw []byte) (*peerRedeem, error) {
	var in struct {
		Token   string `json:"token"`
		Webhook struct {
			URL     string `json:"url"`
			Secreto string `json:"secreto"`
		} `json:"webhook"`
		ConectorID json.RawMessage `json:"conector_id"`
	}
	bad := apperr.New(apperr.KindBadGateway, CodeIntegrationUnavailble, "Respuesta inesperada de ArgosISP al canje del enlace.")
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, bad
	}
	out := &peerRedeem{Token: in.Token, Webhook: in.Webhook}
	var str string
	var num json.Number
	switch {
	case json.Unmarshal(in.ConectorID, &str) == nil:
		out.ConectorID = str
	case json.Unmarshal(in.ConectorID, &num) == nil:
		out.ConectorID = num.String()
	}
	out.ConectorID = strings.TrimSpace(out.ConectorID)
	if n := len(out.Token); n < 8 || n > 512 || !printable(out.Token) || !validWebhookURL(out.Webhook.URL, s.allowPrivate) ||
		len(out.Webhook.Secreto) < 16 || len(out.Webhook.Secreto) > 256 || !printable(out.Webhook.Secreto) ||
		out.ConectorID == "" || len(out.ConectorID) > 128 {
		return nil, bad
	}
	if err := s.eco.Peer.CheckURL(ctx, out.Webhook.URL); err != nil {
		return nil, bad
	}
	return out, nil
}
