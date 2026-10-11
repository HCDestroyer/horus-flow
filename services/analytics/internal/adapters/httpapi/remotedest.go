package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// PermStorageManage es el permiso de GET /platform/remote-destinations.
const PermStorageManage = "platform.storage.manage"

// RemoteDestination es un destino remoto de copias (contrato v0
// platform.yaml, platformListRemoteDestinations; ADR-0019, storage.md §4).
// Solo campos públicos: las credenciales (llave SFTP, token OAuth,
// contraseña MEGA) y la clave de crypt nunca salen por la API.
type RemoteDestination struct {
	ID            uuid.UUID
	Kind          string // sftp | gdrive | dropbox | mega
	Name          string
	Status        string // ok | lagging | failing | disabled
	LastSuccessAt *time.Time
	LagSeconds    *int64
}

// remoteDestinationView es la forma JSON del contrato (Timestamp UTC con ms).
type remoteDestinationView struct {
	ID            uuid.UUID `json:"id"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	LastSuccessAt *string   `json:"last_success_at"`
	LagSeconds    *int64    `json:"lag_seconds"`
}

// RemoteDestinations lista los destinos de plataforma configurados.
type RemoteDestinations interface {
	ListRemoteDestinations(ctx context.Context) ([]RemoteDestination, error)
}

// RemoteDestinationsHandler sirve GET /api/v1/platform/remote-destinations
// (lectura de I1 con x-module analytics). La configuración y el estado de
// los destinos (jobs: remote_destinations, docs/services.md) llegan con la
// escritura en I3; hasta entonces no hay fuente y la instalación no tiene
// copia remota: la lista vacía es la respuesta válida del contrato y la UI
// muestra "Sin copia remota configurada".
type RemoteDestinationsHandler struct {
	guard  *authz.Guard
	source RemoteDestinations // nil = sin destinos configurables aún
	logger *slog.Logger
}

// NewRemoteDestinations crea el handler (source puede ser nil).
func NewRemoteDestinations(guard *authz.Guard, source RemoteDestinations, logger *slog.Logger) *RemoteDestinationsHandler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &RemoteDestinationsHandler{guard: guard, source: source, logger: logger}
}

// Mount registra la ruta.
func (h *RemoteDestinationsHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/platform/remote-destinations", h.guard.Platform(PermStorageManage, h.list))
}

func (h *RemoteDestinationsHandler) list(w http.ResponseWriter, r *http.Request) {
	var out []RemoteDestination
	if h.source != nil {
		got, err := h.source.ListRemoteDestinations(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "remote destinations: list", slog.Any("error", err))
			problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
			return
		}
		out = got
	}
	data := make([]remoteDestinationView, 0, len(out))
	for _, d := range out {
		v := remoteDestinationView{ID: d.ID, Kind: d.Kind, Name: d.Name, Status: d.Status, LagSeconds: d.LagSeconds}
		if d.LastSuccessAt != nil {
			ts := d.LastSuccessAt.UTC().Format("2006-01-02T15:04:05.000Z")
			v.LastSuccessAt = &ts
		}
		data = append(data, v)
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}
