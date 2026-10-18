package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
)

// "Ver en ArgosISP": cada respuesta de ArgosISP al pedir el ticket (§1.5)
// se traduce a un error claro; 409 = conector apagado en ArgosISP (1.7.0).
func TestParseArgosTicket(t *testing.T) {
	t.Parallel()
	s := NewIntegrations(IntegrationsOptions{AllowPrivate: false})
	ok, err := s.parseArgosTicket(201, []byte(`{"url":"https://panel.argos.example/sso/entrar#`+strings.Repeat("t", 43)+`","expira_en":"2026-10-18T12:00:00Z"}`), nil)
	if err != nil || ok.URL == "" || ok.ExpiresAt.IsZero() {
		t.Fatalf("ticket = %+v, %v", ok, err)
	}
	for status, want := range map[int]string{401: "rechazó el token", 403: "rechazó el token", 404: "no tiene ese recurso",
		409: "conector con Horus", 500: "HTTP 500"} {
		_, err := s.parseArgosTicket(status, nil, nil)
		ae, isApp := apperr.As(err)
		if !isApp || ae.Kind != apperr.KindBadGateway || !strings.Contains(ae.Detail, want) {
			t.Fatalf("HTTP %d → %v", status, err)
		}
	}
	if _, err := s.parseArgosTicket(0, nil, errors.New("red")); err == nil {
		t.Fatal("sin respuesta → error")
	}
	// URL con el ticket en la consulta (no en el fragmento) → rechazada.
	if _, err := s.parseArgosTicket(201, []byte(`{"url":"https://panel.argos.example/sso/entrar?t=`+strings.Repeat("t", 43)+`"}`), nil); err == nil {
		t.Fatal("ticket en la consulta aceptado")
	}
}
