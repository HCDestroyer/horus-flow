package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Duration es una duración que se serializa como "5m".
type Duration time.Duration

// D devuelve la time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// MarshalJSON implementa json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// UnmarshalJSON implementa json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duración: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil || v <= 0 {
		return fmt.Errorf("duración inválida %q", s)
	}
	*d = Duration(v)
	return nil
}

// Nombres de detector (clave de detection.detector_config).
const (
	DetC2         = "c2_contact"
	DetScan       = "scanning"
	DetFanout     = "fanout"
	DetWatchPorts = "watched_ports"
	DetSMTP       = "smtp"
	DetDDoS       = "ddos"
	DetBeacon     = "beaconing"
	DetSustained  = "sustained_upload"
)

// AllDetectors en orden de evaluación.
var AllDetectors = []string{DetC2, DetScan, DetWatchPorts, DetFanout, DetSMTP, DetDDoS, DetBeacon, DetSustained}

// C2Params: contacto con C2 conocido (traffic-model.md §8).
type C2Params struct {
	Enabled bool `json:"enabled"`
	// Lookback de la marca en ingesta (flows.reputation_hit, buckets de 1 h).
	Lookback Duration `json:"lookback"`
	// RetroWindow del barrido retroactivo sobre flows_raw (≤ retención cruda, 7 d).
	RetroWindow   Duration `json:"retro_window"`
	RetroInterval Duration `json:"retro_interval"`
	// MinIndicatorConfidence (0–100) del indicador.
	MinIndicatorConfidence int `json:"min_indicator_confidence"`
	// Mining: un pool de minería conocido con respuesta ⇒ cryptomining.
	Mining bool `json:"mining"`
}

// ScanParams: escaneo horizontal y vertical.
type ScanParams struct {
	Enabled          bool     `json:"enabled"`
	Window           Duration `json:"window"`
	SynOnlyRatio     float64  `json:"syn_only_ratio"`
	MinDestinations  int      `json:"min_destinations"`
	VerticalMinPorts int      `json:"vertical_min_ports"`
	// VerticalSmallShare: fracción mínima de flujos pequeños (≤ 3 paquetes)
	// hacia el destino del escaneo vertical.
	VerticalSmallShare float64 `json:"vertical_small_share"`
}

// FanoutParams: fan-out disperso (descarta CDN por /24).
type FanoutParams struct {
	Enabled         bool     `json:"enabled"`
	Window          Duration `json:"window"`
	MinDestinations int      `json:"min_destinations"`
	MinNets24       int      `json:"min_nets24"`
	// CommercialFactor multiplica los umbrales para clientes comerciales.
	CommercialFactor float64 `json:"commercial_factor"`
}

// WatchPortsParams: puertos típicos de botnet (dim.watch_port).
type WatchPortsParams struct {
	Enabled         bool     `json:"enabled"`
	Window          Duration `json:"window"`
	MinDestinations int      `json:"min_destinations"`
	// ExcludePorts: 25 lo cubre el detector SMTP.
	ExcludePorts []uint16 `json:"exclude_ports"`
}

// SMTPParams: SMTP saliente directo.
type SMTPParams struct {
	Enabled bool     `json:"enabled"`
	Window  Duration `json:"window"`
	// Servidores SMTP distintos por ventana (1 h) según el tipo de cliente.
	ResidentialServers int `json:"residential_servers"`
	CommercialServers  int `json:"commercial_servers"`
}

// DDoSParams: participación en DDoS.
type DDoSParams struct {
	Enabled    bool     `json:"enabled"`
	Window     Duration `json:"window"`
	MinPPS     float64  `json:"min_pps"`
	MinMinutes int      `json:"min_minutes"`
	MaxTargets int      `json:"max_targets"`
	// Amplificación: UDP saliente a 53, 123, 1900, 11211, 19.
	AmpPorts      []uint16 `json:"amp_ports"`
	AmpMinPPS     float64  `json:"amp_min_pps"`
	AmpMinTargets int      `json:"amp_min_targets"`
}

// BeaconParams: beaconing (I1-30).
type BeaconParams struct {
	Enabled      bool     `json:"enabled"`
	Window       Duration `json:"window"`
	Interval     Duration `json:"interval"`
	MinSpan      Duration `json:"min_span"`
	MinConns     int      `json:"min_connections"`
	MaxCV        float64  `json:"max_cv"`
	MaxMeanBytes float64  `json:"max_mean_bytes"`
	MinPeriod    Duration `json:"min_period"`
	MaxPeriod    Duration `json:"max_period"`
	ExcludePorts []uint16 `json:"exclude_ports"`
	// AllowASNs: destinos que nunca son beaconing (p. ej. NTP/push propios).
	AllowASNs []uint32 `json:"allow_asns"`
}

// SustainedParams: tráfico saliente sostenido (I1-30).
type SustainedParams struct {
	Enabled     bool     `json:"enabled"`
	Window      Duration `json:"window"`
	MinMbps     float64  `json:"min_mbps"`
	UploadShare float64  `json:"upload_share"`
	MinMinutes  int      `json:"min_minutes"`
	// CloudASNs: copias de seguridad a nubes conocidas.
	CloudASNs []uint32 `json:"cloud_asns"`
	// CloudShare: fracción de la subida hacia CloudASNs para considerarla copia.
	CloudShare float64 `json:"cloud_share"`
	// BackupHours: franja local del ISP (inicio, fin) en que la subida a una
	// nube conocida es una copia de seguridad (falso positivo conocido).
	BackupFrom int `json:"backup_from_hour"`
	BackupTo   int `json:"backup_to_hour"`
}

// Params son los parámetros de todos los detectores de un ISP.
type Params struct {
	C2         C2Params         `json:"c2_contact"`
	Scan       ScanParams       `json:"scanning"`
	Fanout     FanoutParams     `json:"fanout"`
	WatchPorts WatchPortsParams `json:"watched_ports"`
	SMTP       SMTPParams       `json:"smtp"`
	DDoS       DDoSParams       `json:"ddos"`
	Beacon     BeaconParams     `json:"beaconing"`
	Sustained  SustainedParams  `json:"sustained_upload"`
	// AutoExpire cierra (auto_expired) un hallazgo sin ocurrencias en este plazo.
	AutoExpire Duration `json:"auto_expire"`
}

// DefaultParams son los umbrales iniciales absolutos de traffic-model.md §8
// (I1: sin línea base por cliente todavía), alineados con las señales del
// simulador (tools/flowsim/internal/signals).
func DefaultParams() Params {
	return Params{
		C2: C2Params{Enabled: true, Lookback: Duration(2 * time.Hour), RetroWindow: Duration(7 * 24 * time.Hour),
			RetroInterval: Duration(6 * time.Hour), MinIndicatorConfidence: 50, Mining: true},
		Scan: ScanParams{Enabled: true, Window: Duration(5 * time.Minute), SynOnlyRatio: 0.7, MinDestinations: 100,
			VerticalMinPorts: 50, VerticalSmallShare: 0.5},
		Fanout: FanoutParams{Enabled: true, Window: Duration(5 * time.Minute), MinDestinations: 500, MinNets24: 200,
			CommercialFactor: 2},
		WatchPorts: WatchPortsParams{Enabled: true, Window: Duration(5 * time.Minute), MinDestinations: 20,
			ExcludePorts: []uint16{25}},
		SMTP: SMTPParams{Enabled: true, Window: Duration(time.Hour), ResidentialServers: 20, CommercialServers: 200},
		DDoS: DDoSParams{Enabled: true, Window: Duration(5 * time.Minute), MinPPS: 2000, MinMinutes: 2, MaxTargets: 3,
			AmpPorts: []uint16{53, 123, 1900, 11211, 19}, AmpMinPPS: 1000, AmpMinTargets: 20},
		Beacon: BeaconParams{Enabled: true, Window: Duration(24 * time.Hour), Interval: Duration(time.Hour),
			MinSpan: Duration(6 * time.Hour), MinConns: 10, MaxCV: 0.2, MaxMeanBytes: 4096,
			MinPeriod: Duration(30 * time.Second), MaxPeriod: Duration(time.Hour), ExcludePorts: []uint16{53, 123, 853}},
		Sustained: SustainedParams{Enabled: true, Window: Duration(time.Hour), MinMbps: 5, UploadShare: 0.8, MinMinutes: 30,
			// AWS, Google, Microsoft, Apple, Dropbox, Backblaze, Wasabi.
			CloudASNs:  []uint32{16509, 14618, 15169, 396982, 8075, 714, 6185, 19679, 40401, 395747},
			CloudShare: 0.8, BackupFrom: 22, BackupTo: 7},
		AutoExpire: Duration(7 * 24 * time.Hour),
	}
}

// Merge aplica los overrides JSON de un detector (detection.detector_config)
// sobre p. Campos desconocidos = error.
func (p *Params) Merge(detector string, raw json.RawMessage) error {
	var dst any
	switch detector {
	case DetC2:
		dst = &p.C2
	case DetScan:
		dst = &p.Scan
	case DetFanout:
		dst = &p.Fanout
	case DetWatchPorts:
		dst = &p.WatchPorts
	case DetSMTP:
		dst = &p.SMTP
	case DetDDoS:
		dst = &p.DDoS
	case DetBeacon:
		dst = &p.Beacon
	case DetSustained:
		dst = &p.Sustained
	default:
		return fmt.Errorf("detector desconocido %q", detector)
	}
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("parámetros de %s: %w", detector, err)
	}
	return nil
}

// SetEnabled activa o desactiva un detector.
func (p *Params) SetEnabled(detector string, on bool) {
	switch detector {
	case DetC2:
		p.C2.Enabled = on
	case DetScan:
		p.Scan.Enabled = on
	case DetFanout:
		p.Fanout.Enabled = on
	case DetWatchPorts:
		p.WatchPorts.Enabled = on
	case DetSMTP:
		p.SMTP.Enabled = on
	case DetDDoS:
		p.DDoS.Enabled = on
	case DetBeacon:
		p.Beacon.Enabled = on
	case DetSustained:
		p.Sustained.Enabled = on
	}
}
