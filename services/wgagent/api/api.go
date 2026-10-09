// Package api es el contrato público del módulo wg-agent (ADR-0025 §1):
// refleja `horus.wireguard.v1.AgentService` y `ControlService`
// (packages/protobuf/horus/wireguard/v1/agent.proto, generado en agentv1).
// Si wireguard y wg-agent comparten proceso (desarrollo, tests) se llaman por
// estas interfaces registradas en module.Services; si no, por gRPC con mTLS.
package api

import (
	"context"

	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
)

// Nombres en module.Services.
const (
	// ServiceAgent → Agent (lo registra wg-agent).
	ServiceAgent = "wgagent.AgentService"
	// ServiceControl → Control (lo registra wireguard).
	ServiceControl = "wireguard.ControlService"
)

// Agent aplica el estado deseado del hub (idempotente, estado completo +
// versión; fail-static).
type Agent interface {
	ApplyDesiredState(ctx context.Context, req *agentv1.ApplyDesiredStateRequest) (*agentv1.ApplyDesiredStateResponse, error)
}

// Control recibe el estado observado del hub cada 15 s.
type Control interface {
	ReportStatus(ctx context.Context, req *agentv1.ReportStatusRequest) (*agentv1.ReportStatusResponse, error)
}

// HubKey lo implementa un agente en proceso: clave pública del hub (la
// privada nunca sale del agente). El contrato gRPC v0 no la transporta: entre
// procesos se configura en wireguard con HORUS_WG_HUB_PUBLIC_KEY.
type HubKey interface {
	HubPublicKey() string
}
