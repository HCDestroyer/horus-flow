package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// NetwatchReader lee `/tool netwatch` de un router (adaptador RouterOS, solo GET).
type NetwatchReader interface {
	ReadNetwatch(ctx context.Context, t ReadTarget) ([]devapi.NetwatchProbe, error)
}

// probeStore es lo que necesita RouterProbes del repositorio.
type probeStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
}

// apiCredentials descifra la credencial de la API RouterOS.
type apiCredentials interface {
	APICredential(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (*domain.Credential, string, error)
}

// RouterProbes implementa devapi.RouterProbeReader para el rol snmp: lee las
// sondas netwatch por el túnel con el usuario de solo lectura y la huella TLS
// ya fijada (la fija la importación de prefijos, que es interactiva; en
// segundo plano nunca se acepta una huella nueva).
type RouterProbes struct {
	store  probeStore
	creds  apiCredentials
	reader NetwatchReader
	pins   *TLSPins
}

// WithTLSPins hace que una huella sin fijar o distinta de la fijada quede
// registrada como pendiente de confirmación (aviso en la ficha del router);
// nunca se acepta sola.
func (p *RouterProbes) WithTLSPins(pins *TLSPins) *RouterProbes { p.pins = pins; return p }

// NewRouterProbes crea el lector.
func NewRouterProbes(store probeStore, creds apiCredentials, reader NetwatchReader) *RouterProbes {
	return &RouterProbes{store: store, creds: creds, reader: reader}
}

var _ devapi.RouterProbeReader = (*RouterProbes)(nil)

// ReadNetwatch implementa devapi.RouterProbeReader.
func (p *RouterProbes) ReadNetwatch(ctx context.Context, tenant, routerID uuid.UUID) ([]devapi.NetwatchProbe, error) {
	t := pgdb.TenantID(tenant)
	r, err := p.store.GetRouter(ctx, t, routerID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, devapi.ErrRouterNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.TunnelAddress == nil || *r.TunnelAddress == "" {
		return nil, fmt.Errorf("%w: no tunnel", devapi.ErrRouterAPIUnavailable)
	}
	cred, password, err := p.creds.APICredential(ctx, t, routerID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("%w: no API credential", devapi.ErrRouterAPIUnavailable)
	}
	if err != nil {
		return nil, err
	}
	if cred.TLSFingerprintSHA256 == nil || *cred.TLSFingerprintSHA256 == "" {
		if cred.TLSPending == nil {
			p.observe(ctx, t, r, cred)
		}
		return nil, fmt.Errorf("%w: TLS fingerprint not pinned yet (confirm it in the router page)", devapi.ErrRouterAPIUnavailable)
	}
	probes, err := p.reader.ReadNetwatch(ctx, ReadTarget{TunnelIP: *r.TunnelAddress, User: cred.Username, Password: password,
		Pinned: *cred.TLSFingerprintSHA256})
	if errors.Is(err, ErrRouterFingerprint) {
		p.observe(ctx, t, r, cred)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", devapi.ErrRouterAPIUnavailable, err)
	}
	return probes, nil
}

// observe captura la huella que presenta el router (solo el saludo TLS) y la
// deja pendiente de confirmación. Los fallos solo se registran: la lectura en
// segundo plano ya devuelve ErrRouterAPIUnavailable.
func (p *RouterProbes) observe(ctx context.Context, t pgdb.TenantID, r *domain.Router, cred *domain.Credential) {
	if p.pins == nil || p.pins.prober == nil {
		return
	}
	fp, err := p.pins.prober.ProbeFingerprint(ctx, *r.TunnelAddress)
	if err != nil {
		return
	}
	if _, err := p.pins.Observe(ctx, nil, t, r, cred, fp); err != nil {
		p.pins.logger.WarnContext(ctx, "devices: observed TLS fingerprint not recorded", "error", err)
	}
}
