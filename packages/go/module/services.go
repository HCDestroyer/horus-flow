package module

import (
	"fmt"
	"sync"
)

// Services es el registro en proceso de las implementaciones de contratos
// entre módulos (ADR-0025 §1: llamada síncrona "por la interfaz del
// contrato; implementación en proceso cuando el módulo proveedor está en el
// mismo proceso"). El proveedor registra su implementación en su Register
// con un nombre definido en su paquete `api/`; el consumidor la busca en el
// suyo (el catálogo de roles registra a los proveedores antes que al
// gateway). Si no está, el consumidor usa su cliente remoto o se degrada.
type Services struct {
	mu sync.RWMutex
	m  map[string]any
}

// NewServices crea un registro vacío.
func NewServices() *Services { return &Services{m: map[string]any{}} }

// Provide registra v con el nombre name. Falla si ya existe.
func (s *Services) Provide(name string, v any) error {
	if s == nil {
		return fmt.Errorf("module: no services registry for %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; ok {
		return fmt.Errorf("module: service %q already provided", name)
	}
	s.m[name] = v
	return nil
}

// Get devuelve lo registrado con name.
func (s *Services) Get(name string) (any, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[name]
	return v, ok
}

// Lookup devuelve el servicio name con tipo T.
func Lookup[T any](s *Services, name string) (T, bool) {
	var zero T
	v, ok := s.Get(name)
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}
