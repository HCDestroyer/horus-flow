package flowinv

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// WatchFile recarga path en st cuando cambia su fecha o tamaño. Un fichero
// inválido se rechaza y sigue el snapshot anterior. Termina al cancelar ctx.
func WatchFile(ctx context.Context, path string, st *Store, every time.Duration, log *slog.Logger) {
	var lastMod time.Time
	var lastSize int64 = -1
	if fi, err := os.Stat(path); err == nil {
		lastMod, lastSize = fi.ModTime(), fi.Size()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(path)
		if err != nil || (fi.ModTime().Equal(lastMod) && fi.Size() == lastSize) {
			continue
		}
		lastMod, lastSize = fi.ModTime(), fi.Size()
		s, err := LoadFile(path)
		if err != nil {
			log.Warn("flows inventory reload rejected", "path", path, "error", err)
			continue
		}
		st.Swap(s)
		log.Info("flows inventory reloaded", "path", path, "exporters", len(s.data.Exporters), "prefixes", len(s.data.Prefixes))
	}
}
