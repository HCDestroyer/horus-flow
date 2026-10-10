package loadkit

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Scenario es el escenario del simulador que usa una prueba (SCENARIO):
// prefijos de clientes que se dan de alta y opciones de flowsim.
type Scenario struct {
	Name     string
	Prefixes []string
	SimArgs  []string
}

// Scenarios conocidos por las pruebas de carga y de fallo.
var Scenarios = map[string]Scenario{
	// normal: 250 hogares en 10.20.0.0/24, sin NAT ni IPv6 (atribución directa; I1-26).
	"normal": {Name: "normal", Prefixes: []string{ClientPrefix}, SimArgs: []string{"-nat=false", "-ipv6=false"}},
	// isp10k: 10 000 clientes tras NAT (IE 225-228) y ~3 000 /64 delegados,
	// a tasa fija (-flat: sin perfil diario).
	"isp10k": {Name: "isp10k", Prefixes: []string{"10.64.0.0/18", "2001:db8:1000::/40"}, SimArgs: []string{"-flat"}},
}

// ScenarioFromEnv devuelve el escenario de SCENARIO (normal por defecto).
func ScenarioFromEnv() (Scenario, error) {
	name := os.Getenv("SCENARIO")
	if name == "" {
		name = "normal"
	}
	sc, ok := Scenarios[name]
	if !ok {
		return Scenario{}, fmt.Errorf("SCENARIO %q desconocido (normal, isp10k)", name)
	}
	return sc, nil
}

// NATSDiskBytes es lo que ocupa en disco el almacén JetStream del contenedor
// de NATS (con compresión s2, menos que los bytes de TLM_FLOWS).
func (e Env) NATSDiskBytes(ctx context.Context) (uint64, error) {
	out, err := Docker(ctx, "exec", e.Container("nats"), "du", "-sk", "/data/jetstream")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(out)
	if len(f) == 0 {
		return 0, fmt.Errorf("du: %q", out)
	}
	kb, err := strconv.ParseUint(f[0], 10, 64)
	return kb * 1024, err
}
