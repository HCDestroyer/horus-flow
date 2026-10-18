// Package grpcapi expone el agente como horus.tunnels.v1.TunnelAgentService y
// adapta el cliente gRPC de TunnelControlService (wireguard) a api.Control.
package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tunapi "github.com/hcdestroyer/horus-flow/services/tunnelagent/api"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// Server implementa TunnelAgentServiceServer.
type Server struct {
	tunnelv1.UnimplementedTunnelAgentServiceServer
	agent tunapi.Agent
}

// Register registra el servicio en s.
func Register(s *grpc.Server, agent tunapi.Agent) {
	tunnelv1.RegisterTunnelAgentServiceServer(s, &Server{agent: agent})
}

// ApplyTunnels implementa TunnelAgentServiceServer. Los errores no llevan
// detalles (podrían citar identidades); el agente los registra.
func (s *Server) ApplyTunnels(ctx context.Context, req *tunnelv1.ApplyTunnelsRequest) (*tunnelv1.ApplyTunnelsResponse, error) {
	resp, err := s.agent.ApplyTunnels(ctx, req)
	switch {
	case err == nil:
		return resp, nil
	case errors.Is(err, domain.ErrInvalidState):
		return nil, status.Error(codes.InvalidArgument, "invalid desired state")
	case errors.Is(err, app.ErrWrongHub):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return nil, status.Error(codes.Unavailable, "tunnel state not fully applied")
}

// Control adapta TunnelControlServiceClient a api.Control.
type Control struct {
	C tunnelv1.TunnelControlServiceClient
}

var _ tunapi.Control = Control{}

// ReportTunnelStatus implementa api.Control.
func (c Control) ReportTunnelStatus(ctx context.Context, req *tunnelv1.ReportTunnelStatusRequest) (*tunnelv1.ReportTunnelStatusResponse, error) {
	return c.C.ReportTunnelStatus(ctx, req) //nolint:wrapcheck // error gRPC tal cual
}
