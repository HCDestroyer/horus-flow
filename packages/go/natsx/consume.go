package natsx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Message es un mensaje entregado a un handler con el tenant ya validado.
type Message struct {
	Subject string
	Header  nats.Header
	Data    []byte
	// Tenant es el ISP del mensaje (uuid.Nil en tipos de plataforma).
	Tenant uuid.UUID
	// Envelope es el sobre (solo en consumidores de dominio).
	Envelope *Envelope
	// Delivered es el número de entrega (1 = primera).
	Delivered uint64
}

// Type devuelve el tipo del evento (sobre o cabecera Horus-Type).
func (m *Message) Type() string {
	if m.Envelope != nil {
		return m.Envelope.Type
	}
	return m.Header.Get(HeaderType)
}

// Handler procesa un mensaje. Devuelve nil para confirmarlo, un error
// [Permanent] para enviarlo a la DLQ o cualquier otro error para reintentar.
type Handler func(ctx context.Context, m *Message) error

type permanentErr struct{ err error }

func (e *permanentErr) Error() string { return e.err.Error() }
func (e *permanentErr) Unwrap() error { return e.err }

// Permanent marca un error que no se arregla reintentando (DLQ + Term).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentErr{err: err}
}

// IsPermanent indica si err es [Permanent].
func IsPermanent(err error) bool {
	var p *permanentErr
	return errors.As(err, &p)
}

// Encoding del cuerpo de un consumidor.
const (
	// EncodingEnvelope: sobre JSON de dominio (tenant en sobre y cabecera).
	EncodingEnvelope = "envelope"
	// EncodingTelemetry: cuerpo opaco con el sobre en cabeceras
	// (Horus-Tenant es la única fuente del tenant).
	EncodingTelemetry = "telemetry"
)

// ConsumerConfig describe un durable pull (docs/events.md §4.2).
type ConsumerConfig struct {
	Service        string // devices, alerts… (subject de DLQ)
	Stream         string
	Durable        string
	FilterSubjects []string
	Encoding       string // EncodingEnvelope (por defecto) | EncodingTelemetry
	// AllowPlatform acepta mensajes con Horus-Tenant = platform.
	AllowPlatform bool
	DeliverNew    bool
	MaxDeliver    int
	BackOff       []time.Duration
	MaxAckPending int
	AckWait       time.Duration
	Logger        *slog.Logger
}

func (c *ConsumerConfig) defaults() {
	if c.Encoding == "" {
		c.Encoding = EncodingEnvelope
	}
	if c.MaxDeliver <= 0 {
		c.MaxDeliver = 8
	}
	if c.BackOff == nil {
		c.BackOff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute}
	}
	if len(c.BackOff) >= c.MaxDeliver {
		c.BackOff = c.BackOff[:c.MaxDeliver-1]
	}
	if c.MaxAckPending <= 0 {
		c.MaxAckPending = 64
	}
	if c.AckWait <= 0 {
		c.AckWait = 30 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
}

// EnsureConsumer crea o actualiza el durable (cada servicio crea los suyos al
// arrancar, docs/events.md §4.1).
func EnsureConsumer(ctx context.Context, js jetstream.JetStream, c ConsumerConfig) (jetstream.Consumer, error) {
	c.defaults()
	policy := jetstream.DeliverAllPolicy
	if c.DeliverNew {
		policy = jetstream.DeliverNewPolicy
	}
	cfg := jetstream.ConsumerConfig{
		Durable: c.Durable, Name: c.Durable, AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: policy,
		MaxDeliver: c.MaxDeliver, BackOff: c.BackOff, MaxAckPending: c.MaxAckPending, AckWait: c.AckWait,
	}
	if len(c.FilterSubjects) == 1 {
		cfg.FilterSubject = c.FilterSubjects[0]
	} else {
		cfg.FilterSubjects = c.FilterSubjects
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, c.Stream, cfg)
	if err != nil {
		return nil, fmt.Errorf("natsx: consumer %s/%s: %w", c.Stream, c.Durable, err)
	}
	return cons, nil
}

// Consume ejecuta el durable hasta que ctx se cancela. Si el stream aún no
// existe (lo aprovisiona infrastructure/nats), reintenta cada 5 s.
func Consume(ctx context.Context, js jetstream.JetStream, c ConsumerConfig, h Handler) error {
	c.defaults()
	var cons jetstream.Consumer
	for {
		var err error
		cons, err = EnsureConsumer(ctx, js, c)
		if err == nil {
			break
		}
		c.Logger.WarnContext(ctx, "consumer not ready", slog.String("durable", c.Durable), slog.Any("error", err))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
	for ctx.Err() == nil {
		batch, err := cons.Fetch(32, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.Logger.WarnContext(ctx, "consumer fetch failed", slog.String("durable", c.Durable), slog.Any("error", err))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}
		for msg := range batch.Messages() {
			Process(ctx, js, &c, msg, h)
		}
	}
	return nil
}

// Process valida y entrega un mensaje y lo confirma según el resultado
// (exportado para tests de consumidores).
func Process(ctx context.Context, js jetstream.JetStream, c *ConsumerConfig, msg jetstream.Msg, h Handler) {
	c.defaults()
	meta, _ := msg.Metadata()
	var delivered uint64 = 1
	if meta != nil {
		delivered = meta.NumDelivered
	}
	m, err := decode(c, msg)
	if err != nil {
		c.Logger.WarnContext(ctx, "invalid message", slog.String("durable", c.Durable), slog.String("subject", msg.Subject()), slog.Any("error", err))
		deadLetter(ctx, js, c, msg, delivered, err)
		return
	}
	m.Delivered = delivered
	ctx = MessageContext(ctx, m)
	err = h(ctx, m)
	switch {
	case err == nil:
		_ = msg.Ack()
	case IsPermanent(err) || delivered >= uint64(c.MaxDeliver): //nolint:gosec // MaxDeliver > 0
		c.Logger.ErrorContext(ctx, "message to DLQ", slog.String("durable", c.Durable), slog.String("type", m.Type()), slog.Any("error", err))
		deadLetter(ctx, js, c, msg, delivered, err)
	default:
		c.Logger.WarnContext(ctx, "message retry", slog.String("durable", c.Durable), slog.String("type", m.Type()),
			slog.Uint64("delivered", delivered), slog.Any("error", err))
		_ = msg.Nak()
	}
}

// MessageContext devuelve el contexto de procesamiento de m (consumidor
// 1:1, docs/observability.md §4.2): continúa la traza del mensaje (el
// trace_parent del sobre es la fuente de verdad; si no, la cabecera
// traceparent) con un span nuevo y marca tenant_id y event_id para que
// todos los logs del handler los lleven.
func MessageContext(ctx context.Context, m *Message) context.Context {
	tp := m.Header.Get(observability.HeaderTraceParent)
	if m.Envelope != nil && m.Envelope.TraceParent != nil {
		tp = *m.Envelope.TraceParent
	}
	ctx = observability.ContinueTrace(ctx, tp)
	if m.Tenant != uuid.Nil {
		ctx = observability.WithTenant(ctx, m.Tenant.String())
	}
	id := m.Header.Get(HeaderMsgID)
	if m.Envelope != nil {
		id = m.Envelope.ID.String()
	}
	if id != "" {
		ctx = observability.WithEventID(ctx, id)
	}
	return ctx
}

// ErrTenantInvalid es la causa de DLQ de un mensaje sin tenant válido.
var ErrTenantInvalid = errors.New("tenant_invalid")

func decode(c *ConsumerConfig, msg jetstream.Msg) (*Message, error) {
	hdr := msg.Headers()
	m := &Message{Subject: msg.Subject(), Header: hdr, Data: msg.Data()}
	ht := hdr.Get(HeaderTenant)
	if ht == "" {
		return nil, fmt.Errorf("%w: missing %s", ErrTenantInvalid, HeaderTenant)
	}
	if ht != TenantPlatform {
		id, err := uuid.Parse(ht)
		if err != nil {
			return nil, fmt.Errorf("%w: bad %s", ErrTenantInvalid, HeaderTenant)
		}
		m.Tenant = id
	} else if !c.AllowPlatform {
		return nil, fmt.Errorf("%w: platform message on a tenant consumer", ErrTenantInvalid)
	}
	if c.Encoding == EncodingTelemetry {
		return m, nil
	}
	env, err := DecodeEnvelope(msg.Data())
	if err != nil {
		return nil, err
	}
	switch {
	case env.TenantID == nil && m.Tenant != uuid.Nil,
		env.TenantID != nil && *env.TenantID != m.Tenant:
		return nil, fmt.Errorf("%w: header does not match envelope", ErrTenantInvalid)
	}
	m.Envelope = env
	return m, nil
}

func deadLetter(ctx context.Context, js jetstream.JetStream, c *ConsumerConfig, msg jetstream.Msg, attempts uint64, cause error) {
	dlq := nats.NewMsg("horus.dlq." + c.Service + "." + c.Durable)
	dlq.Data = msg.Data()
	for k, v := range msg.Headers() {
		dlq.Header[k] = v
	}
	reason := cause.Error()
	if len(reason) > 1024 {
		reason = reason[:1024]
	}
	dlq.Header.Set("Horus-Dlq-Original-Subject", msg.Subject())
	dlq.Header.Set("Horus-Dlq-Consumer", c.Durable)
	dlq.Header.Set("Horus-Dlq-Attempts", strconv.FormatUint(attempts, 10))
	dlq.Header.Set("Horus-Dlq-Error", reason)
	dlq.Header.Set("Horus-Dlq-Failed-At", time.Now().UTC().Format(TimeFormat))
	if meta, err := msg.Metadata(); err == nil {
		dlq.Header.Set("Horus-Dlq-Stream", meta.Stream)
		dlq.Header.Set("Horus-Dlq-Stream-Seq", strconv.FormatUint(meta.Sequence.Stream, 10))
		dlq.Header.Set(HeaderMsgID, fmt.Sprintf("dlq:%s:%d", meta.Stream, meta.Sequence.Stream))
	} else {
		dlq.Header.Del(HeaderMsgID)
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := js.PublishMsg(pctx, dlq); err != nil {
		c.Logger.ErrorContext(ctx, "dlq publish failed; message left for redelivery", slog.Any("error", err))
		_ = msg.Nak()
		return
	}
	_ = msg.Term()
}
