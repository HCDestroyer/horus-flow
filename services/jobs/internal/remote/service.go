package remote

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Acciones de auditoría (cadena de plataforma). Nunca llevan secretos.
const (
	AuditCreated        = "platform.storage.destination_created"
	AuditUpdated        = "platform.storage.destination_updated"
	AuditDeleted        = "platform.storage.destination_deleted"
	AuditCredentialsSet = "platform.storage.destination_credentials_set"
	AuditTested         = "platform.storage.destination_tested"
	AuditSyncRequested  = "platform.storage.destination_sync_requested"
)

// Repo es el almacén de destinos y ledger ([Store] en producción).
type Repo interface {
	InstallationID(ctx context.Context) (uuid.UUID, error)
	List(ctx context.Context) ([]Destination, error)
	Get(ctx context.Context, id uuid.UUID) (*Destination, error)
	Create(ctx context.Context, d *Destination) (*Destination, error)
	Update(ctx context.Context, d *Destination, version int, resetLedger bool) (*Destination, error)
	Delete(ctx context.Context, id uuid.UUID, version int) error
	SetCredentials(ctx context.Context, id uuid.UUID, ct, dek []byte, kekID, kind string) (*Destination, error)
	RecordTest(ctx context.Context, id uuid.UUID, pin *string, verified bool) (*Destination, error)
	RecordAttempt(ctx context.Context, id uuid.UUID, at time.Time, errMsg string) error
	Ledger(ctx context.Context, id uuid.UUID) (map[string]LedgerEntry, error)
	RecordCopy(ctx context.Context, id uuid.UUID, version int, e LedgerEntry) error
	ForgetCopy(ctx context.Context, id uuid.UUID, rel string) error
}

// Options configura el [Service].
type Options struct {
	Repo   Repo
	Sealer *envelope.Sealer
	// Recipient es la clave pública age de las copias; nil = sin clave: los
	// destinos se pueden configurar y probar, pero no copian
	// (RecipientMissing explica por qué).
	Recipient        age.Recipient
	RecipientMissing string
	// Root es el almacén local (HORUS_DATA_DIR).
	Root string
	// Interval entre pasadas automáticas (10 min); Settle: antigüedad mínima
	// de un archivo para copiarlo (2 min); LagAfter: retraso a partir del
	// cual el destino está "lagging" (26 h).
	Interval time.Duration
	Settle   time.Duration
	LagAfter time.Duration
	// TestTimeout es el plazo de conexión de la prueba (10 s).
	TestTimeout time.Duration
	Audit       func() (authapi.AuditRecorder, bool)
	Logger      *slog.Logger
	Metrics     prometheus.Registerer
	Now         func() time.Time
	// Dial sustituye a net.Dialer (tests).
	Dial func(ctx context.Context, network, addr string) (netConn, error)
}

// Service son los casos de uso de los destinos remotos y el motor de copia.
type Service struct {
	o Options

	scanMu   sync.Mutex
	lastScan *Scan

	mu      sync.Mutex
	pending map[uuid.UUID]bool
	queue   chan uuid.UUID
	idem    map[string]time.Time
	prefix  string

	lag      *prometheus.GaugeVec
	failures *prometheus.CounterVec
	bytes    *prometheus.CounterVec
}

// NewService crea el servicio y registra sus métricas.
func NewService(o Options) (*Service, error) {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Audit == nil {
		o.Audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	if o.Interval <= 0 {
		o.Interval = 10 * time.Minute
	}
	if o.Settle < 0 {
		o.Settle = 0
	}
	if o.LagAfter <= 0 {
		o.LagAfter = 26 * time.Hour
	}
	if o.TestTimeout <= 0 {
		o.TestTimeout = 10 * time.Second
	}
	s := &Service{o: o, pending: map[uuid.UUID]bool{}, queue: make(chan uuid.UUID, 64), idem: map[string]time.Time{}}
	s.lag = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "horus_remote_sync_lag_seconds",
		Help: "Segundos desde la última copia remota correcta de cada destino habilitado."}, []string{"destination"})
	s.failures = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_remote_sync_failures_total",
		Help: "Copias remotas fallidas por destino y motivo (connect, host_key, auth, write, local, config)."}, []string{"destination", "reason"})
	s.bytes = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_remote_sync_bytes_total",
		Help: "Bytes (en claro) de backups copiados a cada destino remoto."}, []string{"destination"})
	if o.Metrics != nil {
		for _, c := range []prometheus.Collector{s.lag, s.failures, s.bytes} {
			if err := o.Metrics.Register(c); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// Now es el reloj del servicio.
func (s *Service) Now() time.Time { return s.o.Now() }

// AuditMeta son los datos de la petición que van a la auditoría.
type AuditMeta struct {
	IP        string
	UserAgent string
	RequestID string
}

func platformPrincipal(ctx context.Context) (*authz.Principal, error) {
	p := authz.FromContext(ctx)
	if p == nil || p.Scope != authz.ScopePlatform {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(PermManage) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return p, nil
}

func (s *Service) audit(ctx context.Context, p *authz.Principal, meta AuditMeta, action, rid, outcome string, changes map[string]any) {
	rec, ok := s.o.Audit()
	if !ok {
		s.o.Logger.WarnContext(ctx, "jobs audit not recorded: auth not local", slog.String("action", action))
		return
	}
	if changes == nil {
		changes = map[string]any{}
	}
	e := authapi.AuditEntry{OccurredAt: s.o.Now(), ActorType: p.Type, ActorID: p.Subject, ViaPlatform: p.ViaPlatform, Action: action,
		ResourceType: "remote_destination", ResourceID: rid, Scope: "platform", Outcome: outcome, IP: meta.IP,
		UserAgent: meta.UserAgent, RequestID: meta.RequestID, Changes: changes}
	if err := rec.Record(ctx, e); err != nil {
		s.o.Logger.ErrorContext(ctx, "jobs audit failed", slog.String("action", action), slog.Any("error", err))
	}
}

func errNotFound() error {
	return &apperr.Error{Kind: apperr.KindNotFound, Code: CodeNotFound, Title: "Destino remoto no encontrado"}
}

func errNameInUse() error {
	return apperr.Conflict(problem.CodeAlreadyExists, "Ya hay otro destino con ese nombre.")
}

func (s *Service) get(ctx context.Context, id uuid.UUID) (*Destination, error) {
	d, err := s.o.Repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, errNotFound()
	}
	return d, err
}

// View es RemoteDestination (contrato), sin secretos.
type View struct {
	ID                 uuid.UUID `json:"id"`
	Kind               string    `json:"kind"`
	Name               string    `json:"name"`
	Status             string    `json:"status"`
	Host               string    `json:"host"`
	Port               int       `json:"port"`
	Username           string    `json:"username"`
	Path               string    `json:"path"`
	Enabled            bool      `json:"enabled"`
	HasCredentials     bool      `json:"has_credentials"`
	HostKeyFingerprint *string   `json:"host_key_fingerprint"`
	RetentionDays      int       `json:"retention_days"`
	LastSuccessAt      *string   `json:"last_success_at"`
	LastAttemptAt      *string   `json:"last_attempt_at"`
	LastError          *string   `json:"last_error"`
	LagSeconds         *int64    `json:"lag_seconds"`
	FilesPending       *int64    `json:"files_pending"`
	BytesPending       *int64    `json:"bytes_pending"`
	Version            int       `json:"version"`
}

func ts(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.UTC().Format("2006-01-02T15:04:05.000Z")
	return &v
}

// view construye la representación; con sc (y su ledger) calcula los
// pendientes.
func (s *Service) view(ctx context.Context, d *Destination, sc *Scan) View {
	status, lag := ComputeStatus(d, s.o.Now(), s.o.LagAfter)
	v := View{ID: d.ID, Kind: d.Kind, Name: d.Name, Status: status, Host: d.Host, Port: d.Port, Username: d.Username, Path: d.Path,
		Enabled: d.Enabled, HasCredentials: d.HasCredentials(), HostKeyFingerprint: d.HostKeyFingerprint, RetentionDays: d.RetentionDays,
		LastSuccessAt: ts(d.LastSuccessAt), LastAttemptAt: ts(d.LastAttemptAt), LastError: d.LastError, LagSeconds: lag, Version: d.Version}
	if sc != nil {
		if ledger, err := s.o.Repo.Ledger(ctx, d.ID); err == nil {
			p := Diff(sc, ledger, s.o.Now(), time.Duration(d.RetentionDays)*24*time.Hour)
			n, b := int64(len(p.Upload)), p.BytesPending
			v.FilesPending, v.BytesPending = &n, &b
		}
	}
	return v
}

// cachedScan recorre el almacén como mucho una vez por minuto para el
// listado (las copias recorren siempre de nuevo).
func (s *Service) cachedScan() *Scan {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	now := s.o.Now()
	if s.lastScan != nil && now.Sub(s.lastScan.At) < time.Minute {
		return s.lastScan
	}
	sc, err := ScanStore(s.o.Root, now, s.o.Settle)
	if err != nil {
		return nil
	}
	s.lastScan = sc
	return sc
}

func (s *Service) storeScan(sc *Scan) {
	s.scanMu.Lock()
	s.lastScan = sc
	s.scanMu.Unlock()
}

// List lista los destinos con su estado y pendientes.
func (s *Service) List(ctx context.Context) ([]View, error) {
	if _, err := platformPrincipal(ctx); err != nil {
		return nil, err
	}
	ds, err := s.o.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	var sc *Scan
	if len(ds) > 0 {
		sc = s.cachedScan()
	}
	out := make([]View, 0, len(ds))
	for i := range ds {
		out = append(out, s.view(ctx, &ds[i], sc))
	}
	return out, nil
}

// Create da de alta un destino (deshabilitado, sin credenciales).
func (s *Service) Create(ctx context.Context, in CreateInput, meta AuditMeta) (*View, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	d, err := ValidateCreate(in)
	if err != nil {
		return nil, err
	}
	d.ID = uuid.Must(uuid.NewV7())
	out, err := s.o.Repo.Create(ctx, d)
	if errors.Is(err, ErrNameInUse) {
		return nil, errNameInUse()
	}
	if err != nil {
		return nil, err
	}
	s.audit(ctx, p, meta, AuditCreated, out.ID.String(), "success", map[string]any{"kind": out.Kind, "name": out.Name,
		"host": out.Host, "port": out.Port, "username": out.Username, "path": out.Path, "retention_days": out.RetentionDays})
	v := s.view(ctx, out, nil)
	return &v, nil
}

// Update edita un destino (If-Match = version).
func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Patch, meta AuditMeta) (*View, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if version != cur.Version {
		return nil, apperr.PreconditionFailed(s.view(ctx, cur, nil))
	}
	next, ch, err := ApplyPatch(cur, in)
	if err != nil {
		return nil, err
	}
	out, err := s.o.Repo.Update(ctx, next, version, ch.ResetLedger)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, errNotFound()
	case errors.Is(err, ErrVersionChanged):
		if cur, err = s.get(ctx, id); err != nil {
			return nil, err
		}
		return nil, apperr.PreconditionFailed(s.view(ctx, cur, nil))
	case errors.Is(err, ErrNameInUse):
		return nil, errNameInUse()
	case err != nil:
		return nil, err
	}
	if !out.Enabled {
		s.lag.DeleteLabelValues(out.ID.String())
	}
	changes := ch.Fields
	if ch.ResetLedger {
		changes["ledger_reset"] = true
	}
	s.audit(ctx, p, meta, AuditUpdated, id.String(), "success", changes)
	v := s.view(ctx, out, nil)
	return &v, nil
}

// Delete borra el destino y sus credenciales (los archivos remotos se quedan).
func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int, meta AuditMeta) error {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return err
	}
	cur, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if version != cur.Version {
		return apperr.PreconditionFailed(s.view(ctx, cur, nil))
	}
	switch err := s.o.Repo.Delete(ctx, id, version); {
	case errors.Is(err, ErrNotFound):
		return errNotFound()
	case errors.Is(err, ErrVersionChanged):
		if cur, err = s.get(ctx, id); err != nil {
			return err
		}
		return apperr.PreconditionFailed(s.view(ctx, cur, nil))
	case err != nil:
		return err
	}
	s.lag.DeleteLabelValues(id.String())
	s.audit(ctx, p, meta, AuditDeleted, id.String(), "success", map[string]any{"name": cur.Name, "host": cur.Host})
	return nil
}

// SetCredentials guarda la credencial cifrada (write-only).
func (s *Service) SetCredentials(ctx context.Context, id uuid.UUID, c Credentials, meta AuditMeta) (*View, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.get(ctx, id); err != nil {
		return nil, err
	}
	if err := ValidateCredentials(c); err != nil {
		return nil, err
	}
	ct, dek, err := sealCredentials(s.o.Sealer, id, c)
	if err != nil {
		return nil, err
	}
	out, err := s.o.Repo.SetCredentials(ctx, id, ct, dek, s.o.Sealer.KEKID(), c.Kind())
	if errors.Is(err, ErrNotFound) {
		return nil, errNotFound()
	}
	if err != nil {
		return nil, err
	}
	s.audit(ctx, p, meta, AuditCredentialsSet, id.String(), "success", map[string]any{"credential_kind": c.Kind()})
	v := s.view(ctx, out, nil)
	return &v, nil
}

// TestResult es RemoteDestinationTestResult.
type TestResult struct {
	OK                 bool    `json:"ok"`
	Stage              string  `json:"stage"`
	HostKeyFingerprint *string `json:"host_key_fingerprint"`
	HostKeyConfirmed   bool    `json:"host_key_confirmed"`
	Error              *string `json:"error"`
	Destination        *View   `json:"destination,omitempty"`
}

// installationPrefix es la carpeta de esta instalación en los destinos.
func (s *Service) installationPrefix(ctx context.Context) (string, error) {
	s.mu.Lock()
	p := s.prefix
	s.mu.Unlock()
	if p != "" {
		return p, nil
	}
	id, err := s.o.Repo.InstallationID(ctx)
	if err != nil {
		return "", err
	}
	p = "horus-" + id.String()
	s.mu.Lock()
	s.prefix = p
	s.mu.Unlock()
	return p, nil
}

// Test prueba la conexión (TOFU de la clave del servidor, autenticación y
// escritura de un archivo de prueba). Un fallo es ok=false, nunca un 5xx.
func (s *Service) Test(ctx context.Context, id uuid.UUID, confirm string, meta AuditMeta) (*TestResult, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	prefix, err := s.installationPrefix(ctx)
	if err != nil {
		return nil, err
	}
	pinned := ""
	if d.HostKeyFingerprint != nil {
		pinned = *d.HostKeyFingerprint
	}
	policy := &hostKeyPolicy{pinned: pinned, confirm: confirm}
	res := &TestResult{}
	fail := func(stage string, e error) {
		res.OK, res.Stage = false, stage
		msg := display(e)
		res.Error = &msg
	}
	var auth []sshAuth
	if d.HasCredentials() {
		sec, err := openCredentials(s.o.Sealer, d)
		if err == nil {
			auth, err = sec.authMethods()
		}
		if err != nil {
			fail(StageAuth, err)
		}
	}
	verified := false
	if res.Error == nil {
		tctx, cancel := context.WithTimeout(ctx, 3*s.o.TestTimeout)
		sess, err := dial(tctx, dialOptions{Host: d.Host, Port: d.Port, User: d.Username, Auth: auth, Policy: policy,
			Timeout: s.o.TestTimeout, Dial: s.o.Dial})
		if err != nil {
			var de *dialError
			if errors.As(err, &de) {
				fail(de.Stage, de.Err)
			} else {
				fail(StageConnect, err)
			}
		} else {
			if err := sess.probeWrite(path.Join(d.Path, prefix)); err != nil {
				fail(StageWrite, err)
			} else {
				res.OK, res.Stage = true, StageDone
				verified = true
			}
			sess.Close()
		}
		cancel()
	}
	var pin *string
	if policy.presented != "" {
		fp := policy.presented
		res.HostKeyFingerprint = &fp
		res.HostKeyConfirmed = policy.accepted
		if policy.accepted && pinned == "" {
			pin = &fp
		}
	} else if pinned != "" {
		res.HostKeyFingerprint = &pinned
	}
	out, err := s.o.Repo.RecordTest(ctx, id, pin, verified)
	if errors.Is(err, ErrNotFound) {
		return nil, errNotFound()
	}
	if err != nil {
		return nil, err
	}
	v := s.view(ctx, out, nil)
	res.Destination = &v
	outcome := "success"
	if !res.OK {
		outcome = "failure"
	}
	changes := map[string]any{"ok": res.OK, "stage": res.Stage, "host_key_fingerprint": res.HostKeyFingerprint,
		"host_key_pinned": pin != nil}
	if res.Error != nil {
		changes["error"] = *res.Error
	}
	s.audit(ctx, p, meta, AuditTested, id.String(), outcome, changes)
	return res, nil
}

// RequestSync encola una copia inmediata. idemKey repetida = la misma
// petición (202 sin encolar otra).
func (s *Service) RequestSync(ctx context.Context, id uuid.UUID, idemKey string, meta AuditMeta) (*View, error) {
	p, err := platformPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.o.Now()
	key := id.String() + "|" + idemKey
	s.mu.Lock()
	for k, at := range s.idem {
		if now.Sub(at) > 24*time.Hour {
			delete(s.idem, k)
		}
	}
	if _, seen := s.idem[key]; seen {
		s.mu.Unlock()
		v := s.view(ctx, d, nil)
		return &v, nil
	}
	s.mu.Unlock()
	switch {
	case !d.HasCredentials():
		return nil, notReady("El destino no tiene credenciales.")
	case d.HostKeyFingerprint == nil:
		return nil, notReady("Falta confirmar la huella de la clave del servidor (POST .../test).")
	case !d.Enabled:
		return nil, notReady("El destino está deshabilitado.")
	case s.o.Recipient == nil:
		return nil, notReady("La instalación no tiene la clave age de las copias: " + s.o.RecipientMissing)
	}
	s.mu.Lock()
	if s.pending[id] {
		s.mu.Unlock()
		return nil, &apperr.Error{Kind: apperr.KindConflict, Code: CodeSyncRunning, Title: "Ya hay una copia en curso",
			Detail: "Ya hay una copia de este destino en curso o en cola."}
	}
	select {
	case s.queue <- id:
		s.pending[id] = true
		s.idem[key] = now
	default:
		s.mu.Unlock()
		return nil, &apperr.Error{Kind: apperr.KindConflict, Code: CodeSyncRunning, Title: "Ya hay una copia en curso",
			Detail: "La cola de copias está llena; inténtelo más tarde."}
	}
	s.mu.Unlock()
	s.audit(ctx, p, meta, AuditSyncRequested, id.String(), "success", nil)
	v := s.view(ctx, d, nil)
	return &v, nil
}
