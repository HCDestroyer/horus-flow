package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Clases de tráfico remoto (TrafficOrigin del contrato).
const (
	OriginLocal         = "local"
	OriginInternational = "international"
	OriginUnknown       = "unknown"
)

// Motivos de la clase local.
const (
	LocalByCountry = "country"      // organización del ASN en el país del ISP
	LocalByList    = "local_list"   // ASN en la lista local del ISP (IXP, cachés on-net)
	LocalByPrefix  = "local_prefix" // IP remota en un prefijo de la lista local del ISP
)

// ClassifyASN clasifica el extremo remoto de un flujo por su ASN: local si
// está en la lista local del ISP (IXP, cachés como Google GGC, Netflix OCA,
// Meta FNA) o si su organización es del país del ISP; internacional si la
// organización es de otro país; desconocido sin ASN (0) o sin país.
func ClassifyASN(asn uint32, orgCountry, tenantCountry string, localASNs []uint32) (origin, reason string) {
	_, inList := slices.BinarySearch(localASNs, asn)
	return ClassifyOrigin(asn == 0, inList, orgCountry, tenantCountry)
}

// ClassifyOrigin es ClassifyASN sobre filas ya agregadas por país (la
// consulta a ClickHouse agrupa por país del ASN y pertenencia a la lista
// local, sin bajar ASN por ASN).
func ClassifyOrigin(noASN, inLocalList bool, orgCountry, tenantCountry string) (origin, reason string) {
	if noASN {
		return OriginUnknown, ""
	}
	if inLocalList {
		return OriginLocal, LocalByList
	}
	oc := strings.ToUpper(strings.TrimSpace(orgCountry))
	if oc == "" || len(oc) != countryLen || oc == "ZZ" {
		return OriginUnknown, ""
	}
	if tenantCountry != "" && oc == strings.ToUpper(tenantCountry) {
		return OriginLocal, LocalByCountry
	}
	return OriginInternational, ""
}

// ASNVolume es el volumen remoto de un nodo y un día agregado por país de la
// organización del ASN y pertenencia a la lista local.
type ASNVolume struct {
	SiteID    uuid.UUID
	Day       time.Time
	Country   string
	NoASN     bool
	Local     bool
	BytesDown uint64
	BytesUp   uint64
}

// TrafficSplit son los volúmenes por clase.
type TrafficSplit struct {
	LocalBytes         uint64   `json:"local_bytes"`
	LocalByCountry     uint64   `json:"local_by_country_bytes"`
	LocalByList        uint64   `json:"local_by_list_bytes"`
	LocalByPrefix      uint64   `json:"local_by_prefix_bytes"`
	InternationalBytes uint64   `json:"international_bytes"`
	UnknownBytes       uint64   `json:"unknown_bytes"`
	InternationalShare *float64 `json:"international_share"`
	LocalShare         *float64 `json:"local_share"`
}

// Add suma un volumen en su clase.
func (t *TrafficSplit) Add(v ASNVolume, tenantCountry string) {
	b := v.BytesDown + v.BytesUp
	origin, reason := ClassifyOrigin(v.NoASN, v.Local, v.Country, tenantCountry)
	switch origin {
	case OriginLocal:
		t.LocalBytes += b
		if reason == LocalByList {
			t.LocalByList += b
		} else {
			t.LocalByCountry += b
		}
	case OriginInternational:
		t.InternationalBytes += b
	default:
		t.UnknownBytes += b
	}
}

func sub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

// MoveToPrefix reclasifica como local por prefijo un volumen que ya se sumó
// por su ASN (v viene de flows.site_remote_net_1d, agregado por las mismas
// claves que el reparto por ASN): lo resta de su clase y lo suma a local. El
// prefijo manda sobre el ASN.
func (t *TrafficSplit) MoveToPrefix(v ASNVolume, tenantCountry string) {
	b := v.BytesDown + v.BytesUp
	origin, reason := ClassifyOrigin(v.NoASN, v.Local, v.Country, tenantCountry)
	switch origin {
	case OriginLocal:
		t.LocalBytes = sub(t.LocalBytes, b)
		if reason == LocalByList {
			t.LocalByList = sub(t.LocalByList, b)
		} else {
			t.LocalByCountry = sub(t.LocalByCountry, b)
		}
	case OriginInternational:
		t.InternationalBytes = sub(t.InternationalBytes, b)
	default:
		t.UnknownBytes = sub(t.UnknownBytes, b)
	}
	t.LocalBytes += b
	t.LocalByPrefix += b
}

// Finish calcula las proporciones sobre el tráfico clasificado (lo
// desconocido no entra en el denominador).
func (t *TrafficSplit) Finish() {
	known := t.LocalBytes + t.InternationalBytes
	if known == 0 {
		t.InternationalShare, t.LocalShare = nil, nil
		return
	}
	i := float64(t.InternationalBytes) / float64(known)
	l := 1 - i
	t.InternationalShare, t.LocalShare = &i, &l
}
