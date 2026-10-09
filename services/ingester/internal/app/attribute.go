package app

import (
	"net/netip"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

var (
	linkMulticastV6 = netip.MustParsePrefix("ff02::/16")
	cgnat           = netip.MustParsePrefix("100.64.0.0/10")
)

// linkScopeV6: enlace local o multicast de enlace, nunca cliente (§4.8.3).
func linkScopeV6(a netip.Addr) bool {
	return a.Is6() && (a.IsLinkLocalUnicast() || linkMulticastV6.Contains(a))
}

// invalidAddr: direcciones que no pueden aparecer en un flujo real (§4.8.3).
func invalidAddr(a netip.Addr) bool {
	return !a.IsValid() || (a.Is6() && (a.IsUnspecified() || a.IsLoopback()))
}

// privateOrCGNAT: RFC 1918, CGNAT (100.64/10) o ULA (fc00::/7).
func privateOrCGNAT(a netip.Addr) bool {
	return a.IsPrivate() || cgnat.Contains(a)
}

// Exporter es el contexto del exportador de un lote.
type Exporter struct {
	TenantID uuid.UUID
	SiteID   uuid.UUID
	RouterID uuid.UUID
}

// side es el resultado de buscar una IP en los prefijos del nodo.
type side struct {
	p        *flowinv.ClientPrefix
	customer bool // cliente de este nodo
	other    bool // cliente de otro nodo (tránsito)
	infra    bool
	excluded bool
}

func lookup(inv *flowinv.Snapshot, e Exporter, a netip.Addr) side {
	m, ok := inv.Lookup(e.TenantID, e.SiteID, a)
	if !ok {
		return side{}
	}
	s := side{p: m.Prefix}
	switch {
	case m.OtherSite:
		s.other = true
	case m.Prefix.Role == flowinv.RoleCustomers:
		s.customer = true
	case m.Prefix.Role == flowinv.RoleInfrastructure:
		s.infra = true
	case m.Prefix.Role == flowinv.RoleExcluded:
		s.excluded = true
	}
	return s
}

// clientKey es la dirección canónica del cliente: IPv4 /32; IPv6 truncada al
// ipv6_client_len de su prefijo (§4.8.1).
func clientKey(a netip.Addr, p *flowinv.ClientPrefix) netip.Addr {
	if a.Is4() {
		return a
	}
	l := flowinv.DefaultIPv6ClientLen
	if p != nil && p.IPv6ClientLen > 0 {
		l = p.IPv6ClientLen
	}
	pf, err := a.Prefix(l)
	if err != nil {
		return a
	}
	return pf.Addr()
}

// edgeSide decide qué extremo está del lado de los clientes (customer_edge)
// para el modo descubrimiento: interfaz si se conoce, si no la IP privada.
func edgeSide(inv *flowinv.Snapshot, e Exporter, r *flowpb.FlowRecord) (local, remote netip.Addr, localIsSrc bool) {
	role := func(ifIndex uint32) string {
		if i, ok := inv.Interface(e.RouterID, ifIndex); ok {
			return i.FlowRole
		}
		return ""
	}
	in, out := role(r.InputIfIndex), role(r.OutputIfIndex)
	switch {
	case in == flowinv.FlowRoleCustomerEdge || out == flowinv.FlowRoleUpstream:
		return r.SrcIP, r.DstIP, true
	case out == flowinv.FlowRoleCustomerEdge || in == flowinv.FlowRoleUpstream:
		return r.DstIP, r.SrcIP, false
	case privateOrCGNAT(r.DstIP) && !privateOrCGNAT(r.SrcIP):
		return r.DstIP, r.SrcIP, false
	}
	return r.SrcIP, r.DstIP, true
}

// Attribute aplica la regla de traffic-model.md §4.4 (verificada con un
// router real, §4.4.3) sobre la tabla de dirección de §4.6:
//
//  1. src en un prefijo de clientes del nodo → cliente = src, upload;
//  2. si no, postNATDestinationIPv4Address (IE 226) presente, distinta de
//     dst y en un prefijo de clientes → cliente = IE 226, download;
//  3. si no, dst en un prefijo de clientes → cliente = dst, download;
//  4. si no, transit / infrastructure / unknown.
//
// Si ambos extremos (o src e IE 226) son clientes del nodo el flujo es
// internal (una fila para el origen). En IPv6 el paso 2 no ocurre (§4.8.4) y
// el cliente es el prefijo delegado. keep=false: el flujo no se guarda
// (excluded o dirección inválida). Rellena los campos de atribución de row.
func Attribute(inv *flowinv.Snapshot, e Exporter, r *flowpb.FlowRecord, row *Row) (keep bool) {
	if invalidAddr(r.SrcIP) || invalidAddr(r.DstIP) {
		return false
	}
	src, dst := lookup(inv, e, r.SrcIP), lookup(inv, e, r.DstIP)
	if src.excluded || dst.excluded {
		return false
	}
	var nat side
	natIP := r.PostNATDstIP
	natOK := false
	if r.SrcIP.Is4() && natIP.IsValid() && !natIP.IsUnspecified() && natIP != r.DstIP {
		nat = lookup(inv, e, natIP)
		natOK = nat.customer
	}
	row.ClientPort, row.RemotePort = 0, 0
	switch {
	case linkScopeV6(r.SrcIP) || linkScopeV6(r.DstIP):
		row.AttributionStatus, row.Direction = StatusInfrastructure, DirUnknown
		row.RemoteIP = r.DstIP
	case src.customer && (dst.customer || natOK):
		other := r.DstIP
		if !dst.customer {
			other = natIP
		}
		row.AttributionStatus, row.Direction, row.Rule = StatusInternal, DirInternal, RuleInternal
		row.RealmID, row.ClientPrefixID = src.p.RealmID, src.p.ID
		row.ClientIP, row.ClientPort = clientKey(r.SrcIP, src.p), r.SrcPort
		row.RemoteIP, row.RemotePort = other, r.DstPort
	case src.customer:
		row.AttributionStatus, row.Direction, row.Rule = StatusAttributed, DirUpload, RuleUploadSrc
		row.RealmID, row.ClientPrefixID = src.p.RealmID, src.p.ID
		row.ClientIP, row.ClientPort = clientKey(r.SrcIP, src.p), r.SrcPort
		row.RemoteIP, row.RemotePort = r.DstIP, r.DstPort
	case natOK:
		row.AttributionStatus, row.Direction, row.Rule = StatusAttributed, DirDownload, RuleDownloadPostNATDst
		row.RealmID, row.ClientPrefixID = nat.p.RealmID, nat.p.ID
		row.ClientIP, row.ClientPort = natIP, r.DstPort
		if r.HasPostNATDstPort {
			row.ClientPort = r.PostNATDstPort
		}
		row.RemoteIP, row.RemotePort = r.SrcIP, r.SrcPort
	case dst.customer:
		row.AttributionStatus, row.Direction, row.Rule = StatusAttributed, DirDownload, RuleDownloadDst
		row.RealmID, row.ClientPrefixID = dst.p.RealmID, dst.p.ID
		row.ClientIP, row.ClientPort = clientKey(r.DstIP, dst.p), r.DstPort
		row.RemoteIP, row.RemotePort = r.SrcIP, r.SrcPort
	case src.other || dst.other:
		row.AttributionStatus, row.Direction = StatusTransit, DirUnknown
		row.RemoteIP = r.DstIP
	case src.infra || dst.infra:
		row.AttributionStatus, row.Direction = StatusInfrastructure, DirUnknown
		if src.infra {
			row.RemoteIP, row.RemotePort = r.DstIP, r.DstPort
		} else {
			row.RemoteIP, row.RemotePort = r.SrcIP, r.SrcPort
		}
	default:
		// §4.6: IP del lado customer_edge en client_ip ⇒ flows.unattributed_1h
		// (modo descubrimiento, I1-29), solo si parece de clientes: privada,
		// CGNAT/ULA o de un ASN propio del ISP (eligible lo decide el llamador
		// con el snapshot ASN). IPv6 truncada a /64, nunca /128 (§4.8.6).
		row.AttributionStatus, row.Direction = StatusUnknown, DirUnknown
		local, remote, localIsSrc := edgeSide(inv, e, r)
		row.ClientIP, row.RemoteIP = local, remote
		if localIsSrc {
			row.ClientPort, row.RemotePort = r.SrcPort, r.DstPort
		} else {
			row.ClientPort, row.RemotePort = r.DstPort, r.SrcPort
		}
		if local.Is6() {
			row.ClientIP = clientKey(local, nil)
		}
	}
	return true
}
