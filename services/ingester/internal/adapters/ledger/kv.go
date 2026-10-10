// Package ledger guarda en un bucket KV de NATS la composición de los grupos
// de INSERT en curso del ingester (token → batch_id), para que los reintentos
// tras una caída del proceso sean idempotentes (app.Ledger).
package ledger

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Bucket es el bucket KV de los grupos en curso.
const Bucket = "flows_ingester_groups"

// Config es la del bucket (la misma que provisiona infrastructure/nats/kv.yaml).
// TTL = max_age de TLM_FLOWS: pasado ese tiempo el stream ya no reentrega.
// Cada grupo cerrado deja una marca de borrado (~150 B) hasta el TTL: a un
// grupo por segundo, ~15 MB/día; 256 MiB dejan margen de sobra.
func Config() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: Bucket, Description: "Grupos de INSERT en curso del ingester (FLOW)",
		History: 1, TTL: 24 * time.Hour, MaxBytes: 256 << 20, Storage: jetstream.FileStorage}
}

// KV implementa app.Ledger.
type KV struct{ KV jetstream.KeyValue }

// Put guarda los batch_id del grupo token.
func (l KV) Put(ctx context.Context, token string, batchIDs []string) error {
	_, err := l.KV.Put(ctx, token, []byte(strings.Join(batchIDs, "\n")))
	return err
}

// Delete borra el grupo token (confirmado).
func (l KV) Delete(ctx context.Context, token string) error {
	return l.KV.Purge(ctx, token)
}

// Load devuelve los grupos que quedaron sin cerrar.
func (l KV) Load(ctx context.Context) (map[string][]string, error) {
	keys, err := l.KV.ListKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for k := range keys.Keys() {
		e, err := l.KV.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if v := string(e.Value()); v != "" {
			out[k] = strings.Split(v, "\n")
		}
	}
	return out, nil
}
