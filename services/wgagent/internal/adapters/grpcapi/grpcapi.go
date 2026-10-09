// Package grpcapi expone el agente como horus.wireguard.v1.AgentService y
// adapta el cliente gRPC de ControlService (wireguard) a api.Control.
package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/domain"
)

// Server implementa AgentServiceServer.
type Server struct {
	agentv1.UnimplementedAgentServiceServer
	agent wgapi.Agent
}

// Register registra el servicio en s.
func Register(s *grpc.Server, agent wgapi.Agent) {
	agentv1.RegisterAgentServiceServer(s, &Server{agent: agent})
}

// ApplyDesiredState implementa AgentServiceServer.
func (s *Server) ApplyDesiredState(ctx context.Context, req *agentv1.ApplyDesiredStateRequest) (*agentv1.ApplyDesiredStateResponse, error) {
	resp, err := s.agent.ApplyDesiredState(ctx, req)
	switch {
	case err == nil:
		return resp, nil
	case errors.Is(err, domain.ErrInvalidState):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, app.ErrWrongHub):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return nil, status.Error(codes.Unavailable, "wireguard interface not available")
}

// Control adapta ControlServiceClient a api.Control.
type Control struct{ C agentv1.ControlServiceClient }

var _ wgapi.Control = Control{}

// ReportStatus implementa api.Control.
func (c Control) ReportStatus(ctx context.Context, req *agentv1.ReportStatusRequest) (*agentv1.ReportStatusResponse, error) {
	return c.C.ReportStatus(ctx, req) //nolint:wrapcheck // error gRPC tal cual
}
