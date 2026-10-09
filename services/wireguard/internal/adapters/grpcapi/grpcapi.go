// Package grpcapi expone el control como horus.wireguard.v1.ControlService
// (lo llama wg-agent por mTLS) y adapta el cliente gRPC de AgentService a
// wgagent/api.Agent.
package grpcapi

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
