package natsx

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
)

// Relay publica las filas pendientes de `<schema>.outbox` (docs/events.md
// §6.1): orden por `seq`, `FOR UPDATE SKIP LOCKED`, un único relay activo por
// esquema (`pg_try_advisory_xact_lock`), `Nats-Msg-Id` = id (JetStream
// deduplica reenvíos en 20 min) y marca `published_at` tras el PubAck. Si NATS
// no responde, reintenta con backoff hasta 10 s sin perder nada (tras volver NATS,
// lo pendiente sale en ≤ 10 s: medido en make chaos-restart-core).
type Relay struct {
	DB     *pgdb.DB
	Schema string
	JS     jetstream.JetStream
	Logger *slog.Logger
	// Interval entre sondeos sin trabajo (por defecto 1 s).
	Interval time.Duration
	// Batch máximo por transacción (por defecto 100).
	Batch int
	// Retention de las filas publicadas (por defecto 7 días).
	Retention time.Duration
}

type outboxRow struct {
	id      uuid.UUID
	subject string
	headers map[string]string
	payload []byte
}

func (r *Relay) defaults() {
	if r.Interval <= 0 {
		r.Interval = time.Second
	}
	if r.Batch <= 0 {
		r.Batch = 100
	}
	if r.Retention <= 0 {
		r.Retention = 7 * 24 * time.Hour
	}
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
}

// Run publica hasta que ctx se cancela.
func (r *Relay) Run(ctx context.Context) error {
	r.defaults()
	backoff := time.Duration(0)
	lastClean := time.Time{}
	for {
		n, err := r.Once(ctx)
		wait := r.Interval
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			backoff = min(max(2*backoff, time.Second), 10*time.Second)
			wait = backoff
			r.Logger.WarnContext(ctx, "outbox relay: publish failed", slog.String("schema", r.Schema), slog.Any("error", err),
				slog.Duration("retry_in", wait))
		case n >= r.Batch:
			backoff, wait = 0, 0
		default:
			backoff = 0
		}
		if time.Since(lastClean) > time.Hour {
			lastClean = time.Now()
			if err := r.clean(ctx); err != nil && ctx.Err() == nil {
				r.Logger.WarnContext(ctx, "outbox relay: cleanup failed", slog.Any("error", err))
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

func (r *Relay) lockKey() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("horus.outbox." + r.Schema))
	return int64(h.Sum64() >> 1) //nolint:gosec // clave de advisory lock
}

func (r *Relay) table() string { return pgx.Identifier{r.Schema, "outbox"}.Sanitize() }

// Once publica un lote y devuelve cuántas filas publicó.
func (r *Relay) Once(ctx context.Context) (int, error) {
	r.defaults()
	published := 0
	var pubErr error
	err := r.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		var locked bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", r.lockKey()).Scan(&locked); err != nil {
			return fmt.Errorf("natsx: advisory lock: %w", err)
		}
		if !locked {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id, subject, headers, payload FROM `+r.table()+`
			WHERE published_at IS NULL ORDER BY seq LIMIT $1 FOR UPDATE SKIP LOCKED`, r.Batch)
		if err != nil {
			return fmt.Errorf("natsx: select outbox: %w", err)
		}
		var batch []outboxRow
		for rows.Next() {
			var o outboxRow
			if err := rows.Scan(&o.id, &o.subject, &o.headers, &o.payload); err != nil {
				rows.Close()
				return fmt.Errorf("natsx: scan outbox: %w", err)
			}
			batch = append(batch, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("natsx: outbox rows: %w", err)
		}
		var done []uuid.UUID
		for _, o := range batch {
			if err := r.publish(ctx, o); err != nil {
				pubErr = err
				if _, e := tx.Exec(ctx, `UPDATE `+r.table()+` SET attempts = attempts + 1 WHERE id = $1`, o.id); e != nil {
					return errors.Join(err, e)
				}
				break // conserva el orden: el resto espera al siguiente intento
			}
			done = append(done, o.id)
		}
		if len(done) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE `+r.table()+` SET published_at = now() WHERE id = ANY($1)`, done); err != nil {
				return fmt.Errorf("natsx: mark published: %w", err)
			}
		}
		published = len(done)
		return nil // se confirma lo publicado aunque un mensaje fallara
	})
	if err != nil {
		return 0, err
	}
	return published, pubErr
}

func (r *Relay) publish(ctx context.Context, o outboxRow) error {
	m := nats.NewMsg(o.subject)
	m.Data = o.payload
	for k, v := range o.headers {
		m.Header.Set(k, v)
	}
	if m.Header.Get(HeaderMsgID) == "" {
		m.Header.Set(HeaderMsgID, o.id.String())
	}
	if m.Header.Get(HeaderTenant) == "" {
		m.Header.Set(HeaderTenant, TenantPlatform)
	}
	m.Header.Set(HeaderContentType, "application/json")
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := r.JS.PublishMsg(pctx, m, jetstream.WithMsgID(o.id.String())); err != nil {
		lctx := observability.WithEventID(ctx, o.id.String())
		if tp := m.Header.Get(observability.HeaderTraceParent); tp != "" {
			lctx = observability.ContinueTrace(lctx, tp)
		}
		r.Logger.DebugContext(lctx, "outbox relay: publish attempt failed", slog.String("schema", r.Schema),
			slog.String("subject", o.subject), slog.Any("error", err))
		return fmt.Errorf("natsx: publish %s (event %s): %w", o.subject, o.id, err)
	}
	return nil
}

func (r *Relay) clean(ctx context.Context) error {
	return r.DB.PlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM `+r.table()+` WHERE published_at < now() - $1::interval`,
			fmt.Sprintf("%d seconds", int(r.Retention.Seconds())))
		if err != nil {
			return fmt.Errorf("natsx: outbox cleanup: %w", err)
		}
		return nil
	})
}
