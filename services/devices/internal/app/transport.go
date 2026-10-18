package app

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// AuditTransportChanged es la acción auditada al cambiar el transporte de un
// router (docs/tunnels.md §3): siempre lleva el nivel de protección y, para
// un transporte sin cifrado por Internet, la confirmación explícita.
const AuditTransportChanged = "devices.router.transport_changed"

// transportStore es lo que necesita Transport del repositorio.
type transportStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	SetRouterTunnel(ctx context.Context, t pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev Events[domain.Router]) error
	Emit(ctx context.Context, evs []outbox.Event) error
}

// Transport cambia el transporte con el que un router se conecta a Horus
// (wireguard | ipsec | gre | direct) y su nivel de protección. Horus nunca
// escribe en el router: tras el cambio el administrador pega la
// configuración nueva (wireguard: script de alta; ipsec/gre: POST
// /routers/{id}/tunnel-config; direct: POST /routers/{id}/direct-onboarding).
type Transport struct {
	store  transportStore
	deny   []netip.Prefix
	audit  func() (authapi.AuditRecorder, bool)
	logger *slog.Logger
	now    func() time.Time
}

// NewTransport crea el servicio. deny son los rangos que nunca son IP
// pública de un router (túneles de Horus, IPs de la instalación).
func NewTransport(store transportStore, deny []netip.Prefix, audit func() (authapi.AuditRecorder, bool), logger *slog.Logger,
	now func() time.Time,
) *Transport {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &Transport{store: store, deny: deny, audit: audit, logger: logger, now: now}
}

// TransportInput es el cuerpo de PUT /routers/{id}/transport.
type TransportInput struct {
	ConnectionMode string  `json:"connection_mode"`
	RemoteAddress  *string `json:"tunnel_remote_address"`
	RemoteID       *string `json:"tunnel_remote_id"`
	// Path: private | internet (obligatorio para gre y direct).
	Path *string `json:"path"`
	// ConfirmUnencryptedInternet: el administrador acepta que gestión y
	// flujos viajen sin cifrar por Internet (gre/direct con path internet).
	ConfirmUnencryptedInternet bool `json:"confirm_unencrypted_internet"`
}

func optStr(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func eqPtr(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// Set aplica el transporte. Idempotente: repetir el mismo cuerpo no cambia
// nada ni audita.
func (s *Transport) Set(ctx context.Context, routerID uuid.UUID, in TransportInput) (*domain.Router, error) {
	p, t, err := scope(ctx, PermCredentialsWrite)
	if err != nil {
		return nil, err
	}
	r, err := s.store.GetRouter(ctx, t, routerID)
	if err != nil || !siteAllowed(p, PermCredentialsWrite, r.SiteID) {
		return nil, apperr.NotFound(domain.CodeRouterNotFound)
	}
	mode := strings.TrimSpace(in.ConnectionMode)
	f := &fieldErrs{}
	if !slices.Contains(domain.ConnectionModes, mode) {
		return nil, apperr.Validation(apperr.Field("connection_mode", "INVALID_VALUE", "wireguard, ipsec, gre o direct."))
	}
	if !domain.SupportsMode(r.Vendor, mode) {
		return nil, apperr.New(apperr.KindInvalid, domain.CodeConnectionModeNotSupported,
			"Este fabricante no admite ese transporte (MikroTik: wireguard, ipsec, gre; Huawei VRP: ipsec, gre, direct).")
	}
	var remoteAddr, remoteID string
	if v := optStr(in.RemoteAddress); v != "" {
		a, err := netip.ParseAddr(v)
		if err != nil || !domain.ValidTunnelRemoteAddress(a) || managementDenied(a, s.deny) {
			f.add("tunnel_remote_address", "INVALID_VALUE", "IPv4 pública del router (origen de IKE/GRE), sin direcciones especiales ni de Horus.")
		} else {
			remoteAddr = a.Unmap().String()
		}
	}
	if v := optStr(in.RemoteID); v != "" {
		if _, c, ok := domain.ParseIKEID(v); ok {
			remoteID = c
		} else {
			f.add("tunnel_remote_id", "INVALID_VALUE", "Identidad IKE: IPv4, nombre DNS, usuario@dominio o keyid:<id>.")
		}
	}
	path := optStr(in.Path)
	switch mode {
	case domain.ConnectionIPsec:
		if remoteID == "" && remoteAddr != "" {
			remoteID = remoteAddr // identidad por defecto: la IP pública
		}
		if remoteID == "" && optStr(in.RemoteID) == "" {
			f.add("tunnel_remote_id", "REQUIRED", "Indique la IP pública del router o su identidad IKE (nombre DNS o keyid:) si tiene IP dinámica o está tras NAT.")
		}
	case domain.ConnectionGRE:
		if remoteAddr == "" && optStr(in.RemoteAddress) == "" {
			f.add("tunnel_remote_address", "REQUIRED", "GRE necesita la IPv4 pública fija del router (el hub solo acepta GRE de esa IP).")
		}
		if remoteID != "" {
			f.add("tunnel_remote_id", "NOT_APPLICABLE", "GRE no usa identidad IKE.")
		}
	default:
		if in.RemoteAddress != nil || in.RemoteID != nil {
			f.add("tunnel_remote_address", "NOT_APPLICABLE", "Solo IPsec y GRE usan la IP pública o la identidad del router.")
		}
	}
	if !domain.Encrypted(mode) {
		if path != domain.PathPrivate && path != domain.PathInternet {
			f.add("path", "REQUIRED", "Sin cifrado hay que declarar el camino: private (MPLS L3VPN, VRF de gestión, fibra propia) o internet.")
		}
	} else if in.Path != nil && path != domain.PathPrivate && path != domain.PathInternet {
		f.add("path", "INVALID_VALUE", "private o internet.")
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	level := domain.ProtectionFor(mode, path)
	if level == domain.ProtectionUnencryptedInternet && !in.ConfirmUnencryptedInternet {
		return nil, apperr.New(apperr.KindInvalid, domain.CodeUnencryptedConfirmation,
			"Gestión (SNMP) y flujos viajarían SIN CIFRAR por Internet: cualquiera en el camino puede leerlos o suplantar al router. "+
				"Confirme explícitamente (confirm_unencrypted_internet) o elija WireGuard/IPsec.")
	}
	before := struct{ mode, level string }{connectionModeOf(r), domain.ProtectionOf(connectionModeOf(r), r.ProtectionLevel)}
	now := s.now().UTC()
	var changed []string
	err = s.store.SetRouterTunnel(ctx, t, r.ID, func(x *domain.Router) bool {
		changed = nil
		cur := connectionModeOf(x)
		if cur != mode {
			x.ConnectionMode = mode
			changed = append(changed, "connection_mode")
			// El router tiene que pegar una configuración nueva.
			if x.OnboardingState != devapi.OnboardingPending {
				x.OnboardingState = devapi.OnboardingPending
				changed = append(changed, "onboarding_state")
			}
			if mode != domain.ConnectionDirect && x.ManagementAddress != nil {
				x.ManagementAddress = nil
				changed = append(changed, "management_address")
			}
			if mode == domain.ConnectionDirect && x.TunnelAddress != nil {
				// Sin túnel: la IP de túnel deja de ser su identidad (wireguard
				// revoca el peer y la libera con cuarentena).
				x.TunnelAddress, x.WireguardPeerID = nil, nil
				changed = append(changed, "tunnel_address")
			}
		}
		if pl := domain.ProtectionOf(cur, x.ProtectionLevel); pl != level || x.ProtectionLevel == "" {
			x.ProtectionLevel = level
			if pl != level {
				changed = append(changed, "protection_level")
			}
		}
		ra, ri := strPtr(remoteAddr), strPtr(remoteID)
		if !eqPtr(x.TunnelRemoteAddress, ra) {
			x.TunnelRemoteAddress = ra
			changed = append(changed, "tunnel_remote_address")
		}
		if !eqPtr(x.TunnelRemoteID, ri) {
			x.TunnelRemoteID = ri
			changed = append(changed, "tunnel_remote_id")
		}
		if (slices.Contains(changed, "tunnel_remote_address") || slices.Contains(changed, "tunnel_remote_id")) &&
			!slices.Contains(changed, "onboarding_state") && x.OnboardingState != devapi.OnboardingPending {
			// Otra IP o identidad: la configuración del router cambia.
			x.OnboardingState = devapi.OnboardingPending
			changed = append(changed, "onboarding_state")
		}
		if len(changed) == 0 {
			return false
		}
		x.UpdatedAt = now
		return true
	}, func(x *domain.Router) []outbox.Event {
		return []outbox.Event{event(p, t, "horus.devices.router.updated", "router", x.ID, x.Version, now, RouterEventData(x, changed))}
	})
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	out, err := s.store.GetRouter(ctx, t, r.ID)
	if err != nil {
		return nil, mapStoreErr(err, domain.CodeRouterNotFound)
	}
	if len(changed) > 0 {
		changes := map[string]any{
			"connection_mode":  map[string]any{"from": before.mode, "to": mode},
			"protection_level": map[string]any{"from": before.level, "to": level},
			"changed_fields":   changed,
		}
		if !domain.Encrypted(mode) {
			changes["path"] = path
			changes["confirm_unencrypted_internet"] = in.ConfirmUnencryptedInternet
		}
		if remoteAddr != "" {
			changes["tunnel_remote_address"] = remoteAddr
		}
		if remoteID != "" {
			changes["tunnel_remote_id"] = remoteID
		}
		recordAudit(ctx, s.audit, s.store, s.logger, now, p, t, AuditTransportChanged, "router", r.ID.String(), "site:"+r.SiteID.String(), changes)
		if level == domain.ProtectionUnencryptedInternet {
			s.logger.WarnContext(ctx, "router transport without encryption over the Internet (confirmed by an administrator)",
				slog.String("router_id", r.ID.String()), slog.String("connection_mode", mode))
		}
	}
	return out, nil
}
