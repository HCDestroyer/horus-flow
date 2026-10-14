// Package probe hace los chequeos activos de los servicios de
// infraestructura desde el rol snmp: ping ICMP (reutiliza el socket del
// sondeador de routers), conexión TCP y GET HTTP(S) con plazo y estado
// esperado. Del HTTP solo se mira el código de estado: el cuerpo no se lee
// ni se guarda, no se siguen redirecciones y nunca se envían credenciales.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/icmp"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// ICMPProbes es el número de ecos por chequeo ICMP.
const ICMPProbes = 3

// Request es un chequeo activo a ejecutar.
type Request struct {
	Check domain.Check
	// Addr y Port son el destino real: el servidor, o la IP de túnel del
	// router y forward_port si el servicio se sondea por reenvío.
	Addr    netip.Addr
	Port    int
	Timeout time.Duration
}

// Prober ejecuta chequeos activos.
type Prober struct {
	// Pinger nil = sin socket ICMP (el chequeo queda unknown).
	Pinger icmp.Pinger
	// Dialer opcional (tests).
	Dialer *net.Dialer
}

func ms(d time.Duration) *float64 {
	v := float64(d.Microseconds()) / 1000
	return &v
}

// Run ejecuta el chequeo y devuelve la observación (sin histéresis).
func (p *Prober) Run(ctx context.Context, r Request) domain.CheckObservation {
	if r.Timeout <= 0 {
		r.Timeout = 5 * time.Second
	}
	switch r.Check.Type {
	case domain.CheckICMP:
		return p.icmp(ctx, r)
	case domain.CheckTCP:
		return p.tcp(ctx, r)
	case domain.CheckHTTP:
		return p.http(ctx, r)
	}
	return domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonError}
}

func (p *Prober) icmp(ctx context.Context, r Request) domain.CheckObservation {
	if p.Pinger == nil {
		return domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonICMPOff}
	}
	if !r.Addr.Unmap().Is4() {
		return domain.CheckObservation{State: domain.StateUnknown, Reason: domain.ReasonICMPv6}
	}
	res, err := p.Pinger.Ping(ctx, r.Addr, ICMPProbes, r.Timeout)
	if err != nil && res.Sent == 0 {
		return domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonUnreachable}
	}
	if res.Received == 0 || res.AvgRTT == nil {
		return domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonTimeout,
			Detail: fmt.Sprintf("0/%d respuestas", res.Sent)}
	}
	o := domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK, LatencyMs: ms(*res.AvgRTT)}
	if res.Received < res.Sent {
		o.Detail = fmt.Sprintf("%d/%d respuestas", res.Received, res.Sent)
	}
	return o
}

func (p *Prober) dialer(timeout time.Duration) *net.Dialer {
	if p.Dialer != nil {
		d := *p.Dialer
		d.Timeout = timeout
		return &d
	}
	return &net.Dialer{Timeout: timeout}
}

func (p *Prober) tcp(ctx context.Context, r Request) domain.CheckObservation {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	start := time.Now()
	conn, err := p.dialer(r.Timeout).DialContext(ctx, "tcp", netip.AddrPortFrom(r.Addr, uint16(r.Port)).String()) //nolint:gosec // 1..65535 validado
	if err != nil {
		return failure(err)
	}
	lat := time.Since(start)
	_ = conn.Close()
	return domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK, LatencyMs: ms(lat)}
}

func (p *Prober) http(ctx context.Context, r Request) domain.CheckObservation {
	c := r.Check
	https := c.HTTPS != nil && *c.HTTPS
	verify := c.TLSVerify == nil || *c.TLSVerify
	scheme := "http"
	if https {
		scheme = "https"
	}
	path := "/"
	if c.Path != nil && *c.Path != "" {
		path = *c.Path
	}
	u, err := url.Parse(scheme + "://" + net.JoinHostPort(r.Addr.String(), strconv.Itoa(r.Port)) + path)
	if err != nil {
		return domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonError}
	}
	tr := &http.Transport{
		DialContext:            p.dialer(r.Timeout).DialContext,
		TLSClientConfig:        &tls.Config{InsecureSkipVerify: !verify, MinVersion: tls.VersionTLS12}, //nolint:gosec // tls_verify=false lo decide el ISP por chequeo
		DisableKeepAlives:      true,
		Proxy:                  nil, // nunca por el proxy del entorno: es la red del ISP
		TLSHandshakeTimeout:    r.Timeout,
		ResponseHeaderTimeout:  r.Timeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: r.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonError}
	}
	req.Header.Set("User-Agent", "HorusFlow-Monitor/1")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return failure(err)
	}
	lat := time.Since(start)
	_ = resp.Body.Close() // el cuerpo no se lee
	lo, hi := c.StatusRange()
	detail := "HTTP " + strconv.Itoa(resp.StatusCode)
	if resp.StatusCode < lo || resp.StatusCode > hi {
		return domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonHTTPStatus, Detail: detail, LatencyMs: ms(lat)}
	}
	return domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK, Detail: detail, LatencyMs: ms(lat)}
}

// failure clasifica un error de red sin incluir su texto (puede llevar
// direcciones o detalles internos).
func failure(err error) domain.CheckObservation {
	o := domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonError}
	var ne net.Error
	var certErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	var alertErr tls.AlertError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		o.Reason = domain.ReasonTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		o.Reason = domain.ReasonRefused
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		o.Reason = domain.ReasonUnreachable
	case errors.As(err, &certErr), errors.As(err, &recErr), errors.As(err, &alertErr):
		o.Reason = domain.ReasonTLSError
	}
	return o
}
