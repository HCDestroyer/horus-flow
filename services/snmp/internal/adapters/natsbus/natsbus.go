// Package natsbus conecta el rol snmp con NATS JetStream: eventos de dominio
// en SNMP_EVENTS (JSON, publicación confirmada; docs/events.md §8.4),
// telemetría en TLM_SNMP (JSON, sobre en cabeceras), estado de los routers en
// el bucket KV `snmp_router_state` y los consumidores de WIREGUARD_EVENTS
// (señal del túnel) y DEVICES_EVENTS (recarga de objetivos).
package natsbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
)

// Nombres del contrato.
const (
	Source          = "horus/snmp"
	StateBucket     = "snmp_router_state"
	StreamWireGuard = "WIREGUARD_EVENTS"
	StreamDevices   = "DEVICES_EVENTS"
	DurableWG       = "snmp-wireguard"
	DurableDevices  = "snmp-devices"
	SubjectDevice   = "horus.telemetry.snmp.device."
	SubjectIface    = "horus.telemetry.snmp.interface."
)

// Events publica eventos de dominio en SNMP_EVENTS.
type Events struct{ JS jetstream.JetStream }

// Publish implementa app.EventPublisher.
func (e Events) Publish(ctx context.Context, ev app.Event) error {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return err
	}
	env := &natsx.Envelope{Type: ev.Type, Source: Source, Subject: ev.Entity, Time: ev.At, TenantID: ev.TenantID,
		AggregateType: ev.AggregateType, AggregateVersion: ev.Version, Actor: &natsx.Actor{Type: "service", ID: "svc:snmp"}, Data: data}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return natsx.Publish(pctx, e.JS, env)
}

// Telemetry publica muestras en TLM_SNMP.
type Telemetry struct{ JS jetstream.JetStream }

// Publish implementa app.TelemetryPublisher: cuerpo JSON con el sobre en
// cabeceras (Horus-Tenant, Horus-Type, Nats-Msg-Id = batch_id).
func (t Telemetry) Publish(ctx context.Context, s app.Sample) error {
	m := nats.NewMsg(s.Subject)
	m.Data = s.Body
	m.Header.Set(natsx.HeaderMsgID, s.BatchID)
	m.Header.Set(natsx.HeaderContentType, "application/json")
	m.Header.Set(natsx.HeaderType, s.Type)
	m.Header.Set(natsx.HeaderSchemaVersion, "1")
	m.Header.Set(natsx.HeaderSource, Source)
	m.Header.Set(natsx.HeaderTime, s.At.UTC().Format(natsx.TimeFormat))
	m.Header.Set(natsx.HeaderTenant, s.TenantID.String())
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := t.JS.PublishMsg(pctx, m)
	return err
}

// OpenState abre (o crea) el bucket de estado.
func OpenState(ctx context.Context, js jetstream.JetStream) (*State, error) {
	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: StateBucket,
		Description: "Estado observado y última muestra de cada router (rol snmp, I2)", History: 1, Storage: jetstream.FileStorage})
	if err != nil {
		return nil, fmt.Errorf("snmp: state bucket: %w", err)
	}
	return &State{KV: kv}, nil
}

// State implementa app.StateStore sobre el bucket KV.
type State struct{ KV jetstream.KeyValue }

// Load devuelve todo el estado guardado (clave = router_id).
func (s *State) Load(ctx context.Context) (map[uuid.UUID][]byte, error) {
	out := map[uuid.UUID][]byte{}
	keys, err := s.KV.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		id, err := uuid.Parse(k)
		if err != nil {
			continue
		}
		e, err := s.KV.Get(ctx, k)
		if err != nil {
			continue
		}
		out[id] = e.Value()
	}
	return out, nil
}

// Save guarda el estado de un router.
func (s *State) Save(ctx context.Context, id uuid.UUID, b []byte) error {
	_, err := s.KV.Put(ctx, id.String(), b)
	return err
}

// Delete borra el estado de un router (baja).
func (s *State) Delete(ctx context.Context, id uuid.UUID) error {
	err := s.KV.Purge(ctx, id.String())
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil
	}
	return err
}

// RunWireGuard consume handshake_stale/recovered/activated y actualiza la
// señal del túnel del router (durable snmp-wireguard).
func RunWireGuard(ctx context.Context, bus *natsx.Bus, eng *app.Engine, log *slog.Logger) error {
	return natsx.Consume(ctx, bus.JS, natsx.ConsumerConfig{Service: "snmp", Stream: StreamWireGuard, Durable: bus.Durable(DurableWG),
		FilterSubjects: []string{"horus.wireguard.peer.handshake_stale.>", "horus.wireguard.peer.handshake_recovered.>", "horus.wireguard.peer.activated.>"},
		Logger:         log}, func(_ context.Context, m *natsx.Message) error {
		var d struct {
			RouterID uuid.UUID `json:"router_id"`
		}
		if err := json.Unmarshal(m.Envelope.Data, &d); err != nil || d.RouterID == uuid.Nil {
			return natsx.Permanent(errors.New("snmp: wireguard event without router_id"))
		}
		signal := api.SignalOK
		if strings.HasSuffix(m.Envelope.Type, ".handshake_stale") {
			signal = api.SignalStale
		}
		eng.SetWireGuard(m.Tenant, d.RouterID, signal, m.Envelope.Time)
		return nil
	})
}

// RunDevices recarga los objetivos al cambiar un router (durable
// snmp-devices, solo mensajes nuevos: el arranque ya lista todo).
func RunDevices(ctx context.Context, bus *natsx.Bus, eng *app.Engine, log *slog.Logger) error {
	return natsx.Consume(ctx, bus.JS, natsx.ConsumerConfig{Service: "snmp", Stream: StreamDevices, Durable: bus.Durable(DurableDevices),
		FilterSubjects: []string{"horus.devices.router.>"}, DeliverNew: true, Logger: log}, func(_ context.Context, m *natsx.Message) error {
		var d struct {
			ID       uuid.UUID `json:"id"`
			RouterID uuid.UUID `json:"router_id"`
		}
		_ = json.Unmarshal(m.Envelope.Data, &d)
		id := d.ID
		if id == uuid.Nil {
			id = d.RouterID
		}
		eng.RouterChanged(id)
		return nil
	})
}
