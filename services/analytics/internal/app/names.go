package app

import (
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
)

// Names resuelve etiquetas de dimensiones (catálogo y ASN, I1-07).
type Names interface {
	Service(id uuid.UUID) (string, bool)
	Category(id uuid.UUID) (string, bool)
	ASN(asn uint32) (string, bool)
}

// noNames no conoce ninguna etiqueta.
type noNames struct{}

func (noNames) Service(uuid.UUID) (string, bool)  { return "", false }
func (noNames) Category(uuid.UUID) (string, bool) { return "", false }
func (noNames) ASN(uint32) (string, bool)         { return "", false }

func (s *Service) names() Names {
	if s.Names == nil {
		return noNames{}
	}
	return s.Names
}

// ASNNames resuelve nombres de ASN con el snapshot IP→ASN de plataforma (lo publica el rol
// traffic); se sustituye en caliente al recargar. Servicios y categorías no los conoce.
type ASNNames struct {
	snap atomic.Pointer[asn.Snapshot]
}

// Set cambia el snapshot vigente.
func (n *ASNNames) Set(s *asn.Snapshot) { n.snap.Store(s) }

// Service implementa Names.
func (n *ASNNames) Service(uuid.UUID) (string, bool) { return "", false }

// Category implementa Names.
func (n *ASNNames) Category(uuid.UUID) (string, bool) { return "", false }

// ASN implementa Names.
func (n *ASNNames) ASN(a uint32) (string, bool) {
	s := n.snap.Load()
	if s == nil {
		return "", false
	}
	info, ok := s.AS(a)
	if !ok || info.Name == "" {
		return "", false
	}
	return info.Name, true
}
