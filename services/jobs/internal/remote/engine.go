package remote

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// errFileChanged: el archivo cambió mientras se subía (se reintenta en la
// pasada siguiente).
var errFileChanged = errors.New("remote: file changed during upload")

// Run es el motor de copia: una pasada al minuto de arrancar y después cada
// Interval, más las copias pedidas con [Service.RequestSync]. Un destino
// cada vez y los archivos en serie (no satura el enlace del ISP). Lo copiado
// queda en el ledger: tras un reinicio se sigue donde se quedó.
func (s *Service) Run(ctx context.Context) {
	first := time.NewTimer(time.Minute)
	defer first.Stop()
	t := time.NewTicker(s.o.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.SyncAll(ctx)
		case <-t.C:
			s.SyncAll(ctx)
		case id := <-s.queue:
			s.syncByID(ctx, id)
		}
	}
}

// SyncAll copia a todos los destinos habilitados, uno detrás de otro.
func (s *Service) SyncAll(ctx context.Context) {
	ds, err := s.o.Repo.List(ctx)
	if err != nil {
		s.o.Logger.WarnContext(ctx, "remote sync: cannot list destinations", slog.Any("error", err))
		return
	}
	for i := range ds {
		if ctx.Err() != nil {
			return
		}
		d := &ds[i]
		if !d.Ready() {
			s.lag.DeleteLabelValues(d.ID.String())
			continue
		}
		s.mu.Lock()
		if s.pending[d.ID] {
			// Ya en cola por una petición: la atiende la cola.
			s.mu.Unlock()
			continue
		}
		s.pending[d.ID] = true
		s.mu.Unlock()
		s.syncByID(ctx, d.ID)
	}
}

func (s *Service) syncByID(ctx context.Context, id uuid.UUID) {
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	d, err := s.o.Repo.Get(ctx, id)
	if err != nil || !d.Ready() {
		return
	}
	s.SyncDestination(ctx, d)
}

// syncFailure es un fallo de copia con su motivo (etiqueta reason).
type syncFailure struct {
	reason string
	err    error
}

// SyncDestination hace una pasada sobre d: sube lo nuevo o cambiado,
// aplica la retención remota y guarda el resultado. Los fallos solo cambian
// el estado del destino y las métricas.
func (s *Service) SyncDestination(ctx context.Context, d *Destination) {
	start := s.o.Now()
	f := s.syncDestination(ctx, d)
	if ctx.Err() != nil {
		return // apagado: se retoma en el próximo arranque
	}
	dest := d.ID.String()
	if f != nil {
		if errors.Is(f.err, ErrVersionChanged) || errors.Is(f.err, ErrNotFound) {
			return // configuración cambiada a mitad: la próxima pasada usa la nueva
		}
		msg := display(f.err)
		s.failures.WithLabelValues(dest, f.reason).Inc()
		s.o.Logger.WarnContext(ctx, "remote sync failed", slog.String("destination", dest), slog.String("reason", f.reason),
			slog.String("error", msg))
		if err := s.o.Repo.RecordAttempt(ctx, d.ID, start, msg); err != nil {
			s.o.Logger.WarnContext(ctx, "remote sync: cannot record attempt", slog.Any("error", err))
		}
		if d.LastSuccessAt != nil {
			s.lag.WithLabelValues(dest).Set(s.o.Now().Sub(*d.LastSuccessAt).Seconds())
		}
		return
	}
	if err := s.o.Repo.RecordAttempt(ctx, d.ID, start, ""); err != nil {
		s.o.Logger.WarnContext(ctx, "remote sync: cannot record attempt", slog.Any("error", err))
	}
	s.lag.WithLabelValues(dest).Set(0)
}

func (s *Service) syncDestination(ctx context.Context, d *Destination) *syncFailure {
	if s.o.Recipient == nil {
		return &syncFailure{"config", fmt.Errorf("falta la clave pública age de las copias: %s", s.o.RecipientMissing)}
	}
	prefix, err := s.installationPrefix(ctx)
	if err != nil {
		return &syncFailure{"local", fmt.Errorf("no se pudo leer el identificador de la instalación: %w", err)}
	}
	sc, err := ScanStore(s.o.Root, s.o.Now(), s.o.Settle)
	if err != nil {
		return &syncFailure{"local", fmt.Errorf("no se pudo recorrer el almacén local: %w", err)}
	}
	s.storeScan(sc)
	ledger, err := s.o.Repo.Ledger(ctx, d.ID)
	if err != nil {
		return &syncFailure{"local", err}
	}
	plan := Diff(sc, ledger, s.o.Now(), time.Duration(d.RetentionDays)*24*time.Hour, d.Paths)
	if len(plan.Upload) == 0 && len(plan.Delete) == 0 {
		// Todo lo local ya está en el destino según el ledger.
		return nil
	}
	sec, err := openCredentials(s.o.Sealer, d)
	if err != nil {
		return &syncFailure{"config", err}
	}
	auth, err := sec.authMethods()
	if err != nil {
		return &syncFailure{"config", err}
	}
	sess, err := dial(ctx, dialOptions{Host: d.Host, Port: d.Port, User: d.Username, Auth: auth,
		Policy: &hostKeyPolicy{pinned: *d.HostKeyFingerprint}, Timeout: s.o.TestTimeout, Dial: s.o.Dial})
	if err != nil {
		var de *dialError
		if errors.As(err, &de) {
			return &syncFailure{de.Stage, de.Err}
		}
		return &syncFailure{StageConnect, err}
	}
	defer sess.Close()
	dest := d.ID.String()
	for _, lf := range plan.Upload {
		if ctx.Err() != nil {
			return &syncFailure{"local", ctx.Err()}
		}
		e, err := s.uploadFile(sess, d, prefix, lf)
		switch {
		case errors.Is(err, os.ErrNotExist), errors.Is(err, errFileChanged):
			continue // borrado o reescrito en local entre el recorrido y la subida
		case err != nil:
			return &syncFailure{StageWrite, err}
		}
		if err := s.o.Repo.RecordCopy(ctx, d.ID, d.Version, *e); err != nil {
			return &syncFailure{"local", err}
		}
		s.bytes.WithLabelValues(dest).Add(float64(lf.Size))
	}
	for _, e := range plan.Delete {
		if ctx.Err() != nil {
			return &syncFailure{"local", ctx.Err()}
		}
		if err := sess.remove(e.RemoteName); err != nil {
			return &syncFailure{StageWrite, err}
		}
		if err := s.o.Repo.ForgetCopy(ctx, d.ID, e.Rel); err != nil {
			return &syncFailure{"local", err}
		}
	}
	return nil
}

// uploadFile lee el archivo, calcula el SHA-256 del original, lo cifra con
// age en Horus y lo sube de forma atómica. El destino nunca ve el original.
func (s *Service) uploadFile(sess *session, d *Destination, prefix string, lf LocalFile) (*LedgerEntry, error) {
	p := filepath.Join(s.o.Root, filepath.FromSlash(lf.Rel))
	fh, err := os.Open(p) //nolint:gosec // ruta del recorrido del almacén, no de la petición
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("sin permiso de lectura en %s", lf.Rel)
		}
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	if st, err := fh.Stat(); err != nil || st.Size() != lf.Size || !st.ModTime().Truncate(time.Microsecond).Equal(lf.MTime) {
		return nil, errFileChanged
	}
	h := sha256.New()
	name := RemoteName(d.Path, prefix, lf.Rel)
	err = sess.upload(name, func(w io.Writer) error {
		n, err := encryptTo(w, io.TeeReader(io.LimitReader(fh, lf.Size+1), h), s.o.Recipient)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return fmt.Errorf("sin permiso de lectura en %s", lf.Rel)
			}
			return fmt.Errorf("error al subir %s: %s", lf.Rel, sftpReason(err))
		}
		if n != lf.Size {
			return errFileChanged
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &LedgerEntry{Rel: lf.Rel, Size: lf.Size, MTime: lf.MTime, SHA256: h.Sum(nil), RemoteName: name,
		UploadedAt: s.o.Now().UTC()}, nil
}
