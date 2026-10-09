package app

import "github.com/google/uuid"

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
