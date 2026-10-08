// Package expect define el formato de expected.json: lo que un escenario del
// simulador ha emitido (conteos por exportador, clientes, IPs fuera de
// prefijos) y lo que promete (señales y hallazgos esperados). Lo escribe
// flowsim y lo consumen sim-verify, las pruebas del colector/ingester y las
// de detección (I1-10, I1-11).
package expect

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
)

// Schema identifica la versión del formato.
const Schema = "horus.flowsim.expected/v1"

// Expected es el contenido de expected.json.
type Expected struct {
	Schema          string              `json:"schema"`
	Scenario        string              `json:"scenario"`
	Description     string              `json:"description,omitempty"`
	Seed            uint64              `json:"seed"`
	Protocol        string              `json:"protocol"`
	NAT             bool                `json:"nat"`
	IPv6            bool                `json:"ipv6"`
	NATFields       bool                `json:"nat_fields"`
	Fixture         bool                `json:"fixture"`
	Rate            float64             `json:"rate"`
	Start           time.Time           `json:"start"`
	DurationSeconds float64             `json:"duration_seconds"`
	Export          ExportParams        `json:"export"`
	Exporters       []Exporter          `json:"exporters"`
	Indicators      []signals.Indicator `json:"indicators"`
	Signals         []signals.Signal    `json:"signals"`
	Findings        []Finding           `json:"findings"`
	Warnings        []string            `json:"warnings,omitempty"`
}

// ExportParams son los parámetros de Traffic Flow usados.
type ExportParams struct {
	ActiveTimeoutSeconds   float64 `json:"active_timeout_seconds"`
	InactiveTimeoutSeconds float64 `json:"inactive_timeout_seconds"`
	TemplateRefreshPackets int     `json:"template_refresh_packets"`
	TemplateTimeoutSeconds float64 `json:"template_timeout_seconds"`
	MaxDatagram            int     `json:"max_datagram"`
	CollectorPort          uint16  `json:"collector_port"`
}

// Exporter es lo esperado de un router exportador.
type Exporter struct {
	Name        string `json:"name"`
	ExporterIP  string `json:"exporter_ip"`
	CollectorIP string `json:"collector_ip"`
	// SentFrom es la dirección UDP real de envío cuando difiere de
	// exporter_ip (p. ej. 127.0.0.10:49152 en una prueba de loopback).
	SentFrom            string            `json:"sent_from,omitempty"`
	ObservationDomainID uint32            `json:"observation_domain_id"`
	BootTime            time.Time         `json:"boot_time"`
	Interfaces          Interfaces        `json:"interfaces"`
	Prefixes            Prefixes          `json:"prefixes"`
	IPv6ClientLen       int               `json:"ipv6_client_len"`
	Totals              Totals            `json:"totals"`
	ByStatus            map[string]uint64 `json:"by_status"`
	Clients             []Client          `json:"clients"`
	Unattributed        []Unattributed    `json:"unattributed"`
}

// Interfaces son los ifIndex del router simulado.
type Interfaces struct {
	Upstream uint32 `json:"upstream"`
	Tunnel   uint32 `json:"tunnel"`
	Transit  uint32 `json:"transit"`
	Access   string `json:"access"`
}

// Prefixes son los prefijos de cliente del nodo (devices.client_prefix).
type Prefixes struct {
	Customers      []string `json:"customers"`
	Infrastructure []string `json:"infrastructure"`
	Excluded       []string `json:"excluded"`
}

// Totals son los conteos de transporte y de registros.
type Totals struct {
	Datagrams     uint64 `json:"datagrams"`
	TemplateSends uint64 `json:"template_sends"`
	DataRecords   uint64 `json:"data_records"`
	RecordsV4     uint64 `json:"records_v4"`
	RecordsV6     uint64 `json:"records_v6"`
	Bytes         uint64 `json:"bytes"`
	Packets       uint64 `json:"packets"`
}

// Client es un cliente que aparece en los flujos (clave de ADR-0018).
type Client struct {
	Key         string `json:"key"`
	Family      int    `json:"family"`
	Population  string `json:"population"`
	Kind        string `json:"kind"`
	Records     uint64 `json:"records"`
	BytesUp     uint64 `json:"bytes_up"`
	BytesDown   uint64 `json:"bytes_down"`
	PacketsUp   uint64 `json:"packets_up"`
	PacketsDown uint64 `json:"packets_down"`
}

// Unattributed es una IP del lado cliente fuera de los prefijos.
type Unattributed struct {
	IP      string `json:"ip"`
	Records uint64 `json:"records"`
}

// Finding es un hallazgo esperado. Los kinds siguen I1-10/I1-11/I1-30 y son
// provisionales hasta que se congele C8.
type Finding struct {
	Exporter string         `json:"exporter"`
	Client   string         `json:"client"`
	Kind     string         `json:"kind"`
	Severity string         `json:"severity"`
	Signals  []string       `json:"signals"`
	Reasons  map[string]any `json:"reasons"`
}

// Save escribe el fichero con sangría estable.
func (e *Expected) Save(path string) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("expected: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil { //nolint:gosec // fichero de datos no sensible
		return fmt.Errorf("expected: %w", err)
	}
	return nil
}

// Load lee un expected.json.
func Load(path string) (*Expected, error) {
	b, err := os.ReadFile(path) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
	if err != nil {
		return nil, fmt.Errorf("expected: %w", err)
	}
	var e Expected
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("expected %s: %w", path, err)
	}
	if e.Schema != Schema {
		return nil, fmt.Errorf("expected %s: esquema %q no soportado (se espera %q)", path, e.Schema, Schema)
	}
	return &e, nil
}
