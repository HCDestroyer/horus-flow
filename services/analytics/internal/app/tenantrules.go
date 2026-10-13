package app

import (
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// tenantRules indexa las reglas ASN propias de un tenant (Administración ›
// ASN propios) para etiquetar los tops: por ASN (asns, organizations), por
// organización propia y por servicio propio (services, categories).
type tenantRules struct {
	byASN map[uint32]*flowinv.ASNRule
	org   map[string]*flowinv.ASNRule // TenantASNOrgID → regla
	svc   map[string]*flowinv.ASNRule // TenantASNServiceID → regla (reglas sin servicio del catálogo)
}

// rules devuelve las reglas vigentes del tenant (proyección de devices en el inventario).
func (s *Service) rules(tenant uuid.UUID) tenantRules {
	tr := tenantRules{byASN: map[uint32]*flowinv.ASNRule{}, org: map[string]*flowinv.ASNRule{}, svc: map[string]*flowinv.ASNRule{}}
	if s.Inv == nil || tenant == uuid.Nil {
		return tr
	}
	for _, r := range s.Inv.Load().ASNRules(tenant) {
		tr.byASN[r.ASN] = r
		tr.org[catalog.TenantASNOrgID(tenant, r.ASN).String()] = r
		if r.Service == "" {
			tr.svc[catalog.TenantASNServiceID(tenant, r.ASN).String()] = r
		}
	}
	return tr
}

// ASNInfo es GET /asn/{asn}: cómo se llama hoy un ASN.
type ASNInfo struct {
	ASN        uint32
	Name       string
	Country    string
	CatalogOrg *catalog.Organization
	Rule       *flowinv.ASNRule
}

// asnInfoSource es la parte de Names que sabe dar el registro completo de un ASN.
type asnInfoSource interface {
	Info(a uint32) (asn.ASInfo, bool)
}

// Info implementa asnInfoSource.
func (n *ASNNames) Info(a uint32) (asn.ASInfo, bool) {
	s := n.snap.Load()
	if s == nil {
		return asn.ASInfo{}, false
	}
	return s.AS(a)
}

// LookupASN devuelve el nombre del ASN en el dataset público, su organización
// del catálogo y la regla propia del tenant (si las hay).
func (s *Service) LookupASN(tenant uuid.UUID, a uint32) ASNInfo {
	out := ASNInfo{ASN: a}
	if src, ok := s.names().(asnInfoSource); ok {
		if info, ok := src.Info(a); ok {
			out.Name, out.Country = info.Name, info.Country
		}
	} else if name, ok := s.names().ASN(a); ok {
		out.Name = name
	}
	if o, ok := s.catalog().Organization(a); ok {
		out.CatalogOrg = o
		if out.Country == "" {
			out.Country = o.Country
		}
	}
	if len(out.Country) != 2 {
		out.Country = ""
	}
	out.Rule = s.rules(tenant).byASN[a]
	return out
}
