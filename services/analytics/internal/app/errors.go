package app

import "errors"

// Errores de las consultas (los traduce httpapi).
var (
	ErrBadRequest = errors.New("bad request")
	ErrNotFound   = errors.New("not found")
	// ErrNoBackend: ClickHouse no configurado (503 ANALYTICS_UNAVAILABLE).
	ErrNoBackend = errors.New("analytics backend not configured")
)
