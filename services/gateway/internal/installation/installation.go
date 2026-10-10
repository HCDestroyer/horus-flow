// Package installation sirve GET /api/v1/platform/installation (D19: modo de
// acceso, TLS y avisos para la consola de plataforma) y vigila, en el modo de
// TLS externo (proxy inverso de la persona), las peticiones que llegan con
// X-Forwarded-Proto=http o desde una IP fuera de --trusted-proxies.
package installation

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
)

// Códigos de aviso (enum abierto de InstallationAccess.warnings).
const (
	WarnIPOnly         = "ip_only_access"
	WarnSelfSigned     = "self_signed_certificate"
	WarnCertExpiring   = "certificate_expiring"
	WarnProxyProtoHTTP = "proxy_forwarded_proto_http"
	WarnProxyUntrusted = "proxy_untrusted_source"
)

// TLSExternal es HORUS_TLS_MODE del modo detrás de proxy inverso.
const TLSExternal = "external"

// Options configura el handler y el vigilante.
type Options struct {
	AccessMode     string
	PublicBaseURL  string
	AllowedOrigins []string
	WGEndpoint     string
	TLSMode        string
	CertFile       string
	Resolver       *clientip.Resolver
	Logger         *slog.Logger
	Metrics        prometheus.Registerer
	Now            func() time.Time
}

// Warning es un aviso de la consola.
type Warning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type tlsInfo struct {
	Mode              string     `json:"mode"`
	HSTS              bool       `json:"hsts"`
	Issuer            *string    `json:"issuer"`
	NotAfter          *time.Time `json:"not_after"`
	FingerprintSHA256 *string    `json:"fingerprint_sha256"`
}

// Access es la respuesta (contrato InstallationAccess).
type Access struct {
	AccessMode        string    `json:"access_mode"`
	PublicBaseURL     string    `json:"public_base_url"`
	Host              string    `json:"host"`
	AllowedOrigins    []string  `json:"allowed_origins"`
	WireguardEndpoint string    `json:"wireguard_endpoint"`
	TLS               tlsInfo   `json:"tls"`
	Warnings          []Warning `json:"warnings"`
}

type seen struct {
	count  uint64
	last   time.Time
	logged time.Time
	ip     string
}

// Installation agrupa el handler y el vigilante de cabeceras del proxy.
type Installation struct {
	o       Options
	mu      sync.Mutex
	seen    map[string]*seen
	counter *prometheus.CounterVec
}

// New crea el servicio.
func New(o Options) *Installation {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Resolver == nil {
		o.Resolver = clientip.Default()
	}
	if o.AccessMode == "" {
		o.AccessMode = "domain"
		if u, err := url.Parse(o.PublicBaseURL); err == nil {
			if _, err := netip.ParseAddr(u.Hostname()); err == nil {
				o.AccessMode = "ip_only"
			}
		}
	}
	in := &Installation{o: o, seen: map[string]*seen{}}
	if o.Metrics != nil {
		in.counter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "horus_gateway_proxy_warnings_total",
			Help: "Peticiones sospechosas en modo TLS externo (X-Forwarded-Proto=http o fuera de --trusted-proxies).",
		}, []string{"reason"})
		if err := o.Metrics.Register(in.counter); err != nil {
			in.counter = nil
		}
	}
	return in
}

// Wrap vigila las peticiones antes del borde (solo en modo TLS externo).
func (in *Installation) Wrap(next http.Handler) http.Handler {
	if in.o.TLSMode != TLSExternal {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in.Observe(r)
		next.ServeHTTP(w, r)
	})
}

// Observe registra los avisos de una petición. Detrás de Traefik: si el
// salto anterior a Traefik (el último de X-Forwarded-For) no es un proxy de
// confianza, la petición no pasó por el proxy de la persona; si el esquema
// que vio el cliente no es https, el proxy no termina TLS (o se le saltó).
func (in *Installation) Observe(r *http.Request) {
	res := in.o.Resolver
	if !res.FromTrustedProxy(r) {
		return // no viene de Traefik (p. ej. healthcheck local)
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	last := strings.TrimSpace(hops[len(hops)-1])
	if a, err := netip.ParseAddr(last); err == nil && !res.Trusted(a) {
		in.record(WarnProxyUntrusted, last)
		return
	}
	if p := res.Proto(r); p != "" && p != "https" {
		in.record(WarnProxyProtoHTTP, clientip.String(r))
	}
}

func (in *Installation) record(code, ip string) {
	now := in.o.Now()
	in.mu.Lock()
	s := in.seen[code]
	if s == nil {
		s = &seen{}
		in.seen[code] = s
	}
	s.count++
	s.last, s.ip = now, ip
	logIt := now.Sub(s.logged) > time.Minute
	if logIt {
		s.logged = now
	}
	in.mu.Unlock()
	if in.counter != nil {
		in.counter.WithLabelValues(code).Inc()
	}
	if logIt {
		in.o.Logger.Warn("gateway: petición sospechosa detrás del proxy inverso", slog.String("reason", code), slog.String("ip", ip))
	}
}

// Access calcula la respuesta.
func (in *Installation) Access() Access {
	host := in.o.PublicBaseURL
	if u, err := url.Parse(in.o.PublicBaseURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	a := Access{
		AccessMode: in.o.AccessMode, PublicBaseURL: in.o.PublicBaseURL, Host: host,
		AllowedOrigins: in.o.AllowedOrigins, WireguardEndpoint: in.o.WGEndpoint,
		TLS:      tlsInfo{Mode: in.o.TLSMode},
		Warnings: []Warning{},
	}
	if a.AllowedOrigins == nil {
		a.AllowedOrigins = []string{in.o.PublicBaseURL}
	}
	if a.TLS.Mode == "" {
		a.TLS.Mode = "acme"
		if a.AccessMode == "ip_only" {
			a.TLS.Mode = "self_signed"
		}
	}
	a.TLS.HSTS = a.TLS.Mode == "acme" && a.AccessMode != "ip_only"
	if a.AccessMode == "ip_only" {
		a.Warnings = append(a.Warnings, Warning{WarnIPOnly, "info", "Acceso solo por IP: sin dominio, el navegador mostrará un aviso de certificado hasta aceptarlo."})
	}
	if a.TLS.Mode == "self_signed" || a.TLS.Mode == "provided" {
		if cert := readCert(in.o.CertFile); cert != nil {
			fp := fingerprint(cert.Raw)
			na := cert.NotAfter.UTC()
			iss := cert.Issuer.String()
			a.TLS.FingerprintSHA256, a.TLS.NotAfter, a.TLS.Issuer = &fp, &na, &iss
			if left := na.Sub(in.o.Now()); left < 30*24*time.Hour {
				a.Warnings = append(a.Warnings, Warning{WarnCertExpiring, "warning", fmt.Sprintf("El certificado caduca el %s.", na.Format("2006-01-02"))})
			}
		}
		if a.TLS.Mode == "self_signed" {
			msg := "Certificado autogenerado: compruebe la huella SHA-256 en el primer acceso."
			if a.TLS.FingerprintSHA256 != nil {
				msg += " Huella: " + *a.TLS.FingerprintSHA256
			}
			a.Warnings = append(a.Warnings, Warning{WarnSelfSigned, "warning", msg})
		}
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if s := in.seen[WarnProxyUntrusted]; s != nil {
		a.Warnings = append(a.Warnings, Warning{WarnProxyUntrusted, "critical", fmt.Sprintf(
			"%d petición(es) llegaron sin pasar por el proxy inverso de confianza (última desde %s, %s). Revise el cortafuegos y --trusted-proxies.",
			s.count, s.ip, s.last.UTC().Format(time.RFC3339))})
	}
	if s := in.seen[WarnProxyProtoHTTP]; s != nil {
		a.Warnings = append(a.Warnings, Warning{WarnProxyProtoHTTP, "warning", fmt.Sprintf(
			"%d petición(es) con X-Forwarded-Proto=http (última %s): el proxy debe servir Horus solo por HTTPS.",
			s.count, s.last.UTC().Format(time.RFC3339))})
	}
	return a
}

// Handle sirve GET /api/v1/platform/installation (el borde ya exigió
// platform.status.read).
func (in *Installation) Handle(w http.ResponseWriter, _ *http.Request) {
	jsonapi.Write(w, http.StatusOK, in.Access())
}

func readCert(path string) *x509.Certificate {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // ruta de configuración del despliegue
	if err != nil {
		return nil
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil
	}
	return c
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
