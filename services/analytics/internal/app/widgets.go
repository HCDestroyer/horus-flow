package app

import (
	"context"
	"time"

	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
)

// Widgets resuelve los datos de los widgets de tráfico (C9, I1-08).
type Widgets struct {
	svc *Service
	ttl time.Duration
}

// NewWidgets crea el resolutor con caché de ttl.
func NewWidgets(svc *Service, ttl time.Duration) *Widgets { return &Widgets{svc: svc, ttl: ttl} }

// Types implementa tw.Provider.
func (w *Widgets) Types() []string { return nil }

// Resolve implementa tw.Provider.
func (w *Widgets) Resolve(context.Context, tw.Request) (*tw.WidgetData, error) {
	return nil, tw.ErrUnsupportedType
}
