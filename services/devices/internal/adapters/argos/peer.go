package argos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
)

// TenantLimiter espacia las peticiones de un ISP (≤ 10 req/s).
type TenantLimiter interface {
	Wait(ctx context.Context, tenant uuid.UUID) error
}

// Peer implementa app.ArgosPeer: canje del enlace de conexión (§1.4) y
// petición de tickets de autologin (§1.5). Sin redirecciones, con la
// guardia de egreso (https y destinos públicos salvo modo laboratorio) y
// respuestas acotadas. Nunca registra tokens, secretos ni códigos.
type Peer struct {
	HTTP    *http.Client
	Guard   *datasets.EgressGuard
	Limiter TenantLimiter
	Timeout time.Duration
}

var _ app.ArgosPeer = (*Peer)(nil)

const maxPeerResponse = 64 << 10

// CheckURL implementa app.ArgosPeer.
func (p *Peer) CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return ErrNotAllowed
	}
	if p.Guard == nil {
		return nil
	}
	if err := p.Guard.CheckURL(ctx, u); err != nil {
		return ErrNotAllowed
	}
	return nil
}

// RedeemLink implementa app.ArgosPeer.
func (p *Peer) RedeemLink(ctx context.Context, apiOrigin string, body []byte) (int, []byte, error) {
	return p.post(ctx, strings.TrimRight(apiOrigin, "/")+"/api/v1/integraciones/horus/conexion/canjear", "", body)
}

// RequestSSOTicket implementa app.ArgosPeer.
func (p *Peer) RequestSSOTicket(ctx context.Context, tenant uuid.UUID, baseURL, token string, body []byte) (int, []byte, error) {
	if p.Limiter != nil {
		if err := p.Limiter.Wait(ctx, tenant); err != nil {
			return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err) //nolint:errorlint // cancelación
		}
	}
	return p.post(ctx, strings.TrimRight(baseURL, "/")+"/integraciones/horus/sso/tickets", token, body)
}

func (p *Peer) post(ctx context.Context, target, token string, body []byte) (int, []byte, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, ErrNotAllowed
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "horus-flow-argos-connector")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	httpc := p.HTTP
	if httpc == nil {
		httpc = &http.Client{}
	}
	cp := *httpc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var res *http.Response
	if p.Guard != nil {
		res, err = p.Guard.Do(&cp, req)
	} else {
		res, err = cp.Do(req)
	}
	if err != nil {
		if errors.Is(err, datasets.ErrEgressDenied) {
			return 0, nil, ErrNotAllowed
		}
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, redact(err)) //nolint:errorlint // sin la URL completa
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxPeerResponse))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err) //nolint:errorlint // lectura cortada
	}
	return res.StatusCode, raw, nil
}
