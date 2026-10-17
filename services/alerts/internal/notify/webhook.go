package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
)

// Canal webhook (ADR-0030; contrato docs/integrations/argosisp.md §1.3 y §3.4).
const (
	KindWebhook               = "webhook"
	defaultWebhookTimeoutSecs = 10
	eventFindingResolved      = "finding_resolved"
	eventCustomerSecurity     = "customer_security_state_changed"
)

// WebhookBackoff son los reintentos del canal webhook (contrato §1.3): 1 min,
// 5 min, 30 min, 2 h y 12 h; después, fallo definitivo.
var WebhookBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}

// WebhookMaxAttempts = envío inicial + los reintentos de WebhookBackoff.
var WebhookMaxAttempts = len(WebhookBackoff) + 1

// WebhookConfig es WebhookChannelConfig.
type WebhookConfig struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// normalizeWebhook valida la config de un canal webhook (https; http solo en
// modo laboratorio). La guardia de egreso vuelve a comprobar el destino al enviar.
func normalizeWebhook(raw json.RawMessage, f *fieldErrs, allowHTTP bool) json.RawMessage {
	var c WebhookConfig
	if decodeStrict(raw, &c) != nil {
		f.add("config", "INVALID_VALUE", "WebhookChannelConfig: url, timeout_seconds.")
		return nil
	}
	c.URL = strings.TrimSpace(c.URL)
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || len(c.URL) > 2048 ||
		(u.Scheme != "https" && (!allowHTTP || u.Scheme != "http")) {
		f.add("config.url", "INVALID_FORMAT", "URL https del receptor, sin credenciales.")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = defaultWebhookTimeoutSecs
	}
	if c.TimeoutSeconds < 2 || c.TimeoutSeconds > 30 {
		f.add("config.timeout_seconds", "OUT_OF_RANGE", "Entre 2 y 30.")
	}
	out, _ := json.Marshal(c)
	return out
}

// contractEvent traduce un tipo de notificación al `event` del contrato
// (§3.4); "" = no se envía por webhook.
func contractEvent(eventType string) string {
	switch eventType {
	case eventFindingOpened, eventFindingReopened:
		return "finding.opened"
	case eventFindingResolved:
		return "finding.resolved"
	case eventCustomerSecurity:
		return "customer.security_state_changed"
	case eventRouterOffline, eventRouterRecovered, eventRouterCPUHigh, eventRouterMemoryHigh, eventRouterTemperatureHigh,
		eventRouterHardwareFailed:
		return "router.status_changed"
	case eventServiceDown, eventServiceDegraded, eventServiceRecovered:
		return "infra_service.state_changed"
	}
	return ""
}

// WebhookCustomers resuelve el cliente de un evento (devices; opcional).
type WebhookCustomers func() (devapi.CustomerDirectory, bool)

func optStr(data map[string]any, k string) any {
	if v, ok := data[k].(string); ok && v != "" {
		return v
	}
	return nil
}

// webhookData construye `data` del cuerpo (§3.4). Sin IP ni alias del
// cliente salvo personal; con los ids de ArgosISP si el cliente tiene identidad.
func (s *Service) webhookData(ctx context.Context, tenant string, eventType string, data map[string]any, rc renderCtx, personal bool) map[string]any {
	customer := func() (map[string]any, any) {
		id := str(data, "customer_id")
		if id == "" {
			return nil, nil
		}
		c := map[string]any{"id": id}
		var external any
		if s.webhookCustomers != nil {
			if dir, ok := s.webhookCustomers(); ok {
				if ref, err := dir.CustomerRef(ctx, mustUUID(tenant), mustUUID(id)); err == nil {
					if personal {
						c["address"], c["alias"] = ref.Address, ref.Alias
					}
					if ref.External != nil {
						external = map[string]any{"system": ref.External.System, "customer_id": ref.External.CustomerID,
							"service_id": ref.External.ServiceID, "company_id": ref.External.CompanyID}
					}
				}
			}
		}
		return c, external
	}
	switch eventType {
	case eventFindingOpened, eventFindingReopened, eventFindingResolved:
		summary := str(data, "kind")
		if sm, ok := data["summary"].(map[string]any); ok {
			if t, _ := sm["text"].(string); t != "" {
				summary = t
			}
		}
		c, ext := customer()
		return map[string]any{
			"finding": map[string]any{"id": optStr(data, "id"), "kind": optStr(data, "kind"), "severity": optStr(data, "severity"),
				"state": optStr(data, "state"), "summary": summary, "opened_at": optStr(data, "opened_at"),
				"window": map[string]any{"from": optStr(data, "window_from"), "to": optStr(data, "window_to")},
				"reopened": eventType == eventFindingReopened},
			"customer": c, "external": ext,
		}
	case eventCustomerSecurity:
		c, ext := customer()
		return map[string]any{"state": optStr(data, "state"), "previous_state": optStr(data, "previous_state"),
			"open_findings": data["open_findings"], "customer": c, "external": ext}
	case eventServiceDown, eventServiceDegraded, eventServiceRecovered:
		return map[string]any{"service": map[string]any{"id": optStr(data, "service_id"), "name": optStr(data, "name"),
			"status": optStr(data, "state"), "previous_status": optStr(data, "previous_state"), "reason": optStr(data, "reason")}}
	default: // router.status_changed
		name := any(nil)
		if rc.routerName != "" {
			name = rc.routerName
		}
		return map[string]any{"router": map[string]any{"id": optStr(data, "router_id"), "name": name, "status": optStr(data, "state"),
			"previous_status": optStr(data, "previous_state"), "reason": optStr(data, "reason")}}
	}
}

// webhookBody es el cuerpo firmado: {id, event, occurred_at, tenant, data}.
func webhookBody(id, event string, at time.Time, tenant string, data map[string]any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"id": id, "event": event, "occurred_at": at.UTC().Format("2006-01-02T15:04:05.000Z"),
		"tenant": tenant, "data": data})
	return b
}

// WebhookError es un fallo de entrega (HTTP del receptor o red).
type WebhookError struct {
	Status int
	Err    error
}

func (e *WebhookError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("webhook: el receptor respondió HTTP %d", e.Status)
	}
	return "webhook: " + e.Err.Error()
}

func (e *WebhookError) Unwrap() error { return e.Err }

// sendWebhook firma y envía body (un intento). 2xx = entregado.
func (s *Senders) sendWebhook(ctx context.Context, wc WebhookConfig, cr Credentials, id string, body []byte, now time.Time) error {
	if cr.WebhookSecret == "" {
		return errors.New("webhook: falta el secreto (missing_credentials)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wc.URL, bytes.NewReader(body))
	if err != nil {
		return &WebhookError{Err: errors.New("URL no válida")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "horus-flow-webhook")
	webhooksig.Sign(req.Header, []byte(cr.WebhookSecret), id, now, body)
	timeout := time.Duration(wc.TimeoutSeconds) * time.Second
	client := s.client(true, timeout)
	var res *http.Response
	if s.WebhookGuard != nil {
		res, err = s.WebhookGuard.Do(client, req)
	} else {
		res, err = client.Do(req)
	}
	if err != nil {
		if errors.Is(err, datasets.ErrEgressDenied) {
			return &WebhookError{Err: errors.New("destino no permitido (solo https y direcciones públicas)")}
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			return &WebhookError{Err: ue.Err}
		}
		return &WebhookError{Err: err}
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &WebhookError{Status: res.StatusCode}
	}
	return nil
}

// testWebhook es la prueba de conexión: un POST firmado con event = "ping".
func (s *Senders) testWebhook(ctx context.Context, wc WebhookConfig, cr Credentials, tenant string) TestResult {
	if cr.WebhookSecret == "" {
		return fail("missing_credentials", "Falta el secreto del webhook (PUT …/credentials con webhook_secret).")
	}
	id := "ping-" + time.Now().UTC().Format("20060102T150405.000")
	body := webhookBody(id, "ping", time.Now(), tenant, map[string]any{})
	err := s.sendWebhook(ctx, wc, cr, id, body, time.Now())
	var we *WebhookError
	switch {
	case err == nil:
		return TestResult{OK: true}
	case errors.As(err, &we) && we.Status != 0:
		return httpStatus(we.Status)
	case errors.As(err, &we) && strings.Contains(we.Error(), "no permitido"):
		return fail("not_allowed", we.Error())
	case errors.As(err, &we):
		return classify(we.Err)
	}
	return classify(err)
}
