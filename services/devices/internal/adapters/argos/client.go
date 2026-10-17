// Package argos es el cliente HTTP de ArgosISP del conector de identidad
// (contrato docs/integrations/argosisp.md §2.1). Resolución preferida por
// lotes con la IP en el cuerpo (POST /integraciones/horus/resolver, máx. 500);
// si ArgosISP aún no la tiene (404), usa los endpoints actuales
// GET /ip/:ip → GET /clientes/:id y GET /servicios/:id.
//
// Token `Authorization: Bearer atk_…`. Las salidas pasan por la guardia de
// egreso (solo https y destinos públicos, salvo modo laboratorio). Nunca se
// registran el token ni las IPs.
package argos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// MaxBatch es el máximo de IPs por petición de resolución.
const MaxBatch = 500

// Modos de resolución.
const (
	ModeBatch    = "batch"
	ModeFallback = "fallback"
)

// Errores del cliente (centinelas de app para que el caso de uso los distinga).
var (
	// ErrUnavailable: ArgosISP no responde, responde 5xx o tarda demasiado.
	ErrUnavailable = app.ErrArgosUnavailable
	// ErrAuth: token rechazado (401).
	ErrAuth = app.ErrArgosAuth
	// ErrForbidden: el token no tiene los alcances (403).
	ErrForbidden = app.ErrArgosForbidden
	// ErrUnexpected: respuesta inesperada.
	ErrUnexpected = app.ErrArgosUnexpected
	// ErrNotAllowed: el destino no pasa la guardia de egreso.
	ErrNotAllowed = app.ErrArgosNotAllowed
)

// Limiter espacia las peticiones (≤ 10 req/s por ISP).
type Limiter interface {
	Wait(ctx context.Context) error
}

// Client llama a una instancia de ArgosISP.
type Client struct {
	BaseURL string // incluye /api/v1
	Token   string
	HTTP    *http.Client
	Guard   *datasets.EgressGuard
	Limiter Limiter
	Timeout time.Duration
}

// Result es la resolución de una IP.
type Result = app.ArgosResult

var _ app.ArgosResolver = (*Client)(nil)

type clienteJSON struct {
	ID     *int64  `json:"id"`
	Nombre *string `json:"nombre"`
	Codigo *string `json:"codigo"`
	Tipo   *string `json:"tipo"`
}

// planJSON acepta "plan": "Fibra 100" o {"nombre": "Fibra 100"}.
type planJSON struct{ name *string }

func (p *planJSON) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		p.name = &s
		return nil
	}
	var o struct {
		Nombre *string `json:"nombre"`
	}
	if json.Unmarshal(b, &o) == nil {
		p.name = o.Nombre
	}
	return nil
}

// empresaJSON es la empresa del servicio (ArgosISP multiempresa, contrato 1.2.0 §2.1).
type empresaJSON struct {
	ID     *int64  `json:"id"`
	Nombre *string `json:"nombre"`
	NIT    *string `json:"nit"`
}

type servicioJSON struct {
	ID         *int64       `json:"id"`
	UserPPP    *string      `json:"user_ppp"`
	Plan       *planJSON    `json:"plan"`
	PlanNombre *string      `json:"plan_nombre"`
	Estado     *string      `json:"estado"`
	RouterID   *int64       `json:"router_id"`
	Empresa    *empresaJSON `json:"empresa"`
	EmpresaID  *int64       `json:"empresa_id"`
}

func (s *servicioJSON) plan() *string {
	if s == nil {
		return nil
	}
	if s.Plan != nil && s.Plan.name != nil {
		return s.Plan.name
	}
	return s.PlanNombre
}

type resultadoJSON struct {
	IP         string        `json:"ip"`
	ServicioID *int64        `json:"servicio_id"`
	ClienteID  *int64        `json:"cliente_id"`
	Cliente    *clienteJSON  `json:"cliente"`
	Servicio   *servicioJSON `json:"servicio"`
	Desde      *time.Time    `json:"desde"`
	Error      *string       `json:"error"`
	// Empresa: presente desde 1.2.0 en el resolutor por lotes (null = sin
	// empresa); ausente en versiones anteriores y en el respaldo.
	Empresa json.RawMessage `json:"empresa"`
}

// company aplica a out la empresa informada (en el resultado o, en el
// respaldo, si GET /servicios/:id la trae). Sin el campo, CompanyKnown queda
// en false y se conserva la empresa ya guardada.
func (r resultadoJSON) company(out *domain.Resolution) {
	if len(r.Empresa) > 0 {
		out.CompanyKnown = true
		var e *empresaJSON
		if json.Unmarshal(r.Empresa, &e) == nil && e != nil {
			out.CompanyID, out.CompanyName, out.CompanyNIT = e.ID, e.Nombre, e.NIT
		}
		return
	}
	if s := r.Servicio; s != nil && (s.Empresa != nil || s.EmpresaID != nil) {
		out.CompanyKnown, out.CompanyID = true, s.EmpresaID
		if e := s.Empresa; e != nil {
			out.CompanyName, out.CompanyNIT = e.Nombre, e.NIT
			if e.ID != nil {
				out.CompanyID = e.ID
			}
		}
	}
}

func (r resultadoJSON) resolution() domain.Resolution {
	if r.Error != nil || r.ServicioID == nil {
		return domain.Resolution{}
	}
	out := domain.Resolution{Found: true, ExternalCustomerID: r.ClienteID, ExternalServiceID: r.ServicioID, Since: r.Desde}
	if r.Cliente != nil {
		out.DisplayName, out.Code = r.Cliente.Nombre, r.Cliente.Codigo
		if out.ExternalCustomerID == nil {
			out.ExternalCustomerID = r.Cliente.ID
		}
	}
	if r.Servicio != nil {
		out.ServiceState, out.Plan = r.Servicio.Estado, r.Servicio.plan()
	}
	r.company(&out)
	return out
}

// Resolve resuelve las IPs (≤ MaxBatch) y dice qué modo usó.
func (c *Client) Resolve(ctx context.Context, ips []string) ([]Result, string, error) {
	if len(ips) > MaxBatch {
		return nil, "", fmt.Errorf("argos: %d ips (max %d)", len(ips), MaxBatch)
	}
	if len(ips) == 0 {
		return []Result{}, ModeBatch, nil
	}
	body, _ := json.Marshal(map[string]any{"ips": ips})
	status, raw, err := c.do(ctx, http.MethodPost, "/integraciones/horus/resolver", body)
	if err != nil {
		return nil, "", err
	}
	switch {
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		res, err := c.fallback(ctx, ips)
		return res, ModeFallback, err
	case status >= 200 && status < 300:
	default:
		return nil, "", statusErr(status)
	}
	var out struct {
		Resultados []resultadoJSON `json:"resultados"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUnexpected, err) //nolint:errorlint // sin cadena de errores del cuerpo remoto
	}
	byIP := make(map[string]resultadoJSON, len(out.Resultados))
	for _, r := range out.Resultados {
		byIP[r.IP] = r
	}
	res := make([]Result, len(ips))
	for i, ip := range ips {
		res[i] = Result{IP: ip, Resolution: byIP[ip].resolution()}
	}
	return res, ModeBatch, nil
}

// fallback resuelve una a una con GET /ip/:ip, /clientes/:id y /servicios/:id.
func (c *Client) fallback(ctx context.Context, ips []string) ([]Result, error) {
	clientes := map[int64]*clienteJSON{}
	servicios := map[int64]*servicioJSON{}
	out := make([]Result, len(ips))
	for i, ip := range ips {
		out[i].IP = ip
		status, raw, err := c.do(ctx, http.MethodGet, "/ip/"+url.PathEscape(ip), nil)
		if err != nil {
			return nil, err
		}
		if status == http.StatusNotFound {
			continue
		}
		if status < 200 || status > 299 {
			return nil, statusErr(status)
		}
		var r resultadoJSON
		if err := json.Unmarshal(raw, &r); err != nil || r.ServicioID == nil {
			return nil, ErrUnexpected
		}
		if r.ClienteID != nil {
			cl, ok := clientes[*r.ClienteID]
			if !ok {
				cl = &clienteJSON{}
				if err := c.getJSON(ctx, "/clientes/"+strconv.FormatInt(*r.ClienteID, 10), cl); err != nil {
					return nil, err
				}
				clientes[*r.ClienteID] = cl
			}
			r.Cliente = cl
		}
		sv, ok := servicios[*r.ServicioID]
		if !ok {
			sv = &servicioJSON{}
			if err := c.getJSON(ctx, "/servicios/"+strconv.FormatInt(*r.ServicioID, 10), sv); err != nil {
				return nil, err
			}
			servicios[*r.ServicioID] = sv
		}
		r.Servicio = sv
		out[i].Resolution = r.resolution()
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	status, raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil // borrado entre las dos llamadas: sin datos
	}
	if status < 200 || status > 299 {
		return statusErr(status)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return ErrUnexpected
	}
	return nil
}

func statusErr(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return ErrAuth
	case status == http.StatusForbidden:
		return ErrForbidden
	case status == http.StatusTooManyRequests || status >= 500:
		return fmt.Errorf("%w: HTTP %d", ErrUnavailable, status)
	}
	return fmt.Errorf("%w: HTTP %d", ErrUnexpected, status)
}

const maxResponse = 4 << 20

func (c *Client) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if c.Limiter != nil {
		if err := c.Limiter.Wait(ctx); err != nil {
			return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err) //nolint:errorlint // cancelación o límite
		}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnexpected, err) //nolint:errorlint // URL inválida
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "horus-flow-argos-connector")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{}
	}
	cp := *httpc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var res *http.Response
	if c.Guard != nil {
		res, err = c.Guard.Do(&cp, req)
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
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err) //nolint:errorlint // lectura cortada
	}
	return res.StatusCode, raw, nil
}

// redact quita la URL (puede llevar una IP de cliente en el respaldo /ip/:ip)
// del mensaje de error de red.
func redact(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return "error de red"
}
