// Package grpcapi expone el control como horus.wireguard.v1.ControlService
// (lo llama wg-agent por mTLS) y horus.tunnels.v1.TunnelControlService (lo
// llama tunnel-agent), y adapta los clientes gRPC de AgentService y
// TunnelAgentService.
package grpcapi

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tunapi "github.com/hcdestroyer/horus-flow/services/tunnelagent/api"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
)

// Server implementa ControlServiceServer.
type Server struct {
	agentv1.UnimplementedControlServiceServer
	control wgapi.Control
}

// Register registra el servicio en s.
func Register(s *grpc.Server, c wgapi.Control) {
	agentv1.RegisterControlServiceServer(s, &Server{control: c})
}

// ReportStatus implementa ControlServiceServer.
func (s *Server) ReportStatus(ctx context.Context, req *agentv1.ReportStatusRequest) (*agentv1.ReportStatusResponse, error) {
	resp, err := s.control.ReportStatus(ctx, req)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "report not accepted")
	}
	return resp, nil
}

// Agent adapta AgentServiceClient a api.Agent.
type Agent struct{ C agentv1.AgentServiceClient }

var _ wgapi.Agent = Agent{}

// ApplyDesiredState implementa api.Agent.
func (a Agent) ApplyDesiredState(ctx context.Context, req *agentv1.ApplyDesiredStateRequest) (*agentv1.ApplyDesiredStateResponse, error) {
	return a.C.ApplyDesiredState(ctx, req) //nolint:wrapcheck // error gRPC tal cual
}

// TunnelServer implementa horus.tunnels.v1.TunnelControlService (lo llama tunnel-agent).
type TunnelServer struct {
	tunnelv1.UnimplementedTunnelControlServiceServer
	control tunapi.Control
}

// RegisterTunnels registra el control de túneles en s.
func RegisterTunnels(s *grpc.Server, c tunapi.Control) {
	tunnelv1.RegisterTunnelControlServiceServer(s, &TunnelServer{control: c})
}

// ReportTunnelStatus implementa TunnelControlServiceServer.
func (s *TunnelServer) ReportTunnelStatus(ctx context.Context, req *tunnelv1.ReportTunnelStatusRequest) (*tunnelv1.ReportTunnelStatusResponse, error) {
	resp, err := s.control.ReportTunnelStatus(ctx, req)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "report not accepted")
	}
	return resp, nil
}

// TunnelAgent adapta TunnelAgentServiceClient a tunnelagent/api.Agent.
type TunnelAgent struct {
	C tunnelv1.TunnelAgentServiceClient
}

var _ tunapi.Agent = TunnelAgent{}

// ApplyTunnels implementa api.Agent.
func (a TunnelAgent) ApplyTunnels(ctx context.Context, req *tunnelv1.ApplyTunnelsRequest) (*tunnelv1.ApplyTunnelsResponse, error) {
	return a.C.ApplyTunnels(ctx, req) //nolint:wrapcheck // error gRPC tal cual
}
