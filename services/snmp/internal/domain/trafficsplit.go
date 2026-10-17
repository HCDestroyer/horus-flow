package domain

import (
	"slices"
	"strings"
)

// Clases de tráfico remoto (TrafficOrigin del contrato).
const (
	OriginLocal         = "local"
	OriginInternational = "international"
	OriginUnknown       = "unknown"
)

// Motivos de la clase local.
const (
	LocalByCountry = "country"    // organización del ASN en el país del ISP
	LocalByList    = "local_list" // ASN en la lista local del ISP (IXP, cachés on-net)
)

// ClassifyASN clasifica el extremo remoto de un flujo por su ASN: local si
// está en la lista local del ISP (IXP, cachés como Google GGC, Netflix OCA,
// Meta FNA) o si su organización es del país del ISP; internacional si la
// organización es de otro país; desconocido sin ASN (0) o sin país.
func ClassifyASN(asn uint32, orgCountry, tenantCountry string, localASNs []uint32) (origin, reason string) {
	if asn == 0 {
		return OriginUnknown, ""
	}
	if _, ok := slices.BinarySearch(localASNs, asn); ok {
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

// ASNVolume es el volumen de un ASN remoto en una ventana.
type ASNVolume struct {
	ASN        uint32
	Country    string
	Name       string
	BytesDown  uint64
	BytesUp    uint64
	SiteKey    string // "" = total
	BucketUnix int64  // 0 = toda la ventana
}

// TrafficSplit son los volúmenes por clase.
type TrafficSplit struct {
	LocalBytes         uint64   `json:"local_bytes"`
	LocalByCountry     uint64   `json:"local_by_country_bytes"`
	LocalByList        uint64   `json:"local_by_list_bytes"`
	InternationalBytes uint64   `json:"international_bytes"`
	UnknownBytes       uint64   `json:"unknown_bytes"`
	InternationalShare *float64 `json:"international_share"`
	LocalShare         *float64 `json:"local_share"`
}

// Add suma el volumen de un ASN en su clase.
func (t *TrafficSplit) Add(v ASNVolume, tenantCountry string, localASNs []uint32) {
	b := v.BytesDown + v.BytesUp
	origin, reason := ClassifyASN(v.ASN, v.Country, tenantCountry, localASNs)
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
