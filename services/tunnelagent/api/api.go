// Package api es el contrato público del módulo tunnel-agent (ADR-0025 §1,
// ADR-0031): refleja `horus.tunnels.v1.TunnelAgentService` y
// `TunnelControlService` (packages/protobuf/horus/tunnels/v1/agent.proto,
// generado en tunnelv1). Si wireguard y tunnel-agent comparten proceso
// (desarrollo, tests) se llaman por estas interfaces registradas en
// module.Services; si no, por gRPC con mTLS.
package api

import (
	"context"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
)

// Nombres en module.Services.
const (
	// ServiceAgent → Agent (lo registra tunnel-agent).
	ServiceAgent = "tunnelagent.TunnelAgentService"
	// ServiceControl → Control (lo registra wireguard).
	ServiceControl = "wireguard.TunnelControlService"
)

// Agent aplica el estado deseado de los túneles IPsec/GRE del hub
// (idempotente, estado completo + versión; fail-static).
type Agent interface {
	ApplyTunnels(ctx context.Context, req *tunnelv1.ApplyTunnelsRequest) (*tunnelv1.ApplyTunnelsResponse, error)
}

// Control recibe el estado observado de los túneles cada 15 s.
type Control interface {
	ReportTunnelStatus(ctx context.Context, req *tunnelv1.ReportTunnelStatusRequest) (*tunnelv1.ReportTunnelStatusResponse, error)
}
