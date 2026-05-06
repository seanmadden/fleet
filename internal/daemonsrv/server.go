package daemonsrv

import (
	"context"

	fleetv1 "github.com/brizzai/fleet/gen/proto/fleet/v1"
	"github.com/brizzai/fleet/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Server implements fleetv1.FleetServer by routing each RPC into the in-process
// *service.SessionService. Embedding UnimplementedFleetServer makes every RPC
// we don't override return codes.Unimplemented automatically — so we can ship
// the MVP subset (sessions / repos / slots / pins) and fill in SendKeys,
// CapturePane, Workspace*, Config*, and StreamHookEvents in later PRs without
// breaking forward compatibility.
type Server struct {
	fleetv1.UnimplementedFleetServer
	svc service.Service
}

func NewServer(svc service.Service) *Server {
	return &Server{svc: svc}
}

// ── Sessions ───────────────────────────────────────────────────────────────

func (s *Server) GetSession(_ context.Context, req *fleetv1.GetSessionRequest) (*fleetv1.Session, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	sess := s.svc.GetSession(req.GetId())
	if sess == nil {
		return nil, status.Errorf(codes.NotFound, "session %q not found", req.GetId())
	}
	return convertSession(sess), nil
}

func (s *Server) CreateSession(_ context.Context, req *fleetv1.CreateSessionRequest) (*fleetv1.Session, error) {
	if req.GetProjectPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "project_path is required")
	}

	if req.GetForkFromId() != "" {
		// Service.ForkSession takes the *parent's claude session id*, not the
		// fleet session id. We treat fork_from_id as the parent's claude
		// session id verbatim per proto field documentation. Mac/TUI clients
		// are expected to pass the captured Claude session id.
		s2, err := s.svc.ForkSession(req.GetTitle(), req.GetProjectPath(), req.GetWorkspaceName(), req.GetForkFromId())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "fork session: %v", err)
		}
		return convertSession(s2), nil
	}

	s2, err := s.svc.CreateSession(req.GetTitle(), req.GetProjectPath(), req.GetWorkspaceName())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create session: %v", err)
	}
	return convertSession(s2), nil
}

func (s *Server) DeleteSession(_ context.Context, req *fleetv1.DeleteSessionRequest) (*emptypb.Empty, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	// PR 4 ignores DeleteOption (workspace/repo cleanup) and defer_tmux_kill —
	// undo-delete is TUI-internal today; daemon-driven cleanup is PR 5+.
	s.svc.DeleteSession(req.GetId())
	return &emptypb.Empty{}, nil
}

func (s *Server) RestartSession(_ context.Context, req *fleetv1.RestartSessionRequest) (*fleetv1.Session, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.svc.RestartSession(req.GetId()); err != nil {
		return nil, status.Errorf(codes.Internal, "restart: %v", err)
	}
	sess := s.svc.GetSession(req.GetId())
	if sess == nil {
		return nil, status.Errorf(codes.NotFound, "session %q not found post-restart", req.GetId())
	}
	return convertSession(sess), nil
}

func (s *Server) RenameSession(_ context.Context, req *fleetv1.RenameSessionRequest) (*fleetv1.Session, error) {
	if req.GetId() == "" || req.GetTitle() == "" {
		return nil, status.Error(codes.InvalidArgument, "id and title are required")
	}
	s.svc.RenameSession(req.GetId(), req.GetTitle())
	sess := s.svc.GetSession(req.GetId())
	if sess == nil {
		return nil, status.Errorf(codes.NotFound, "session %q not found", req.GetId())
	}
	return convertSession(sess), nil
}

func (s *Server) AcknowledgeSession(_ context.Context, req *fleetv1.AcknowledgeSessionRequest) (*emptypb.Empty, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	s.svc.AcknowledgeSession(req.GetId())
	return &emptypb.Empty{}, nil
}

// ── Repos ──────────────────────────────────────────────────────────────────

func (s *Server) PinRepo(_ context.Context, req *fleetv1.PinRepoRequest) (*emptypb.Empty, error) {
	if req.GetRoot() == "" {
		return nil, status.Error(codes.InvalidArgument, "root is required")
	}
	if err := s.svc.PinRepo(req.GetRoot()); err != nil {
		return nil, status.Errorf(codes.Internal, "pin: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) UnpinRepo(_ context.Context, req *fleetv1.UnpinRepoRequest) (*emptypb.Empty, error) {
	if req.GetRoot() == "" {
		return nil, status.Error(codes.InvalidArgument, "root is required")
	}
	if err := s.svc.UnpinRepo(req.GetRoot()); err != nil {
		return nil, status.Errorf(codes.Internal, "unpin: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// ── Slot bindings ──────────────────────────────────────────────────────────

func (s *Server) ListSlotBindings(_ context.Context, _ *emptypb.Empty) (*fleetv1.ListSlotBindingsResponse, error) {
	return &fleetv1.ListSlotBindingsResponse{Bindings: convertSlotBindings(s.svc.SlotBindings())}, nil
}

func (s *Server) BindSlot(_ context.Context, req *fleetv1.BindSlotRequest) (*emptypb.Empty, error) {
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if req.GetSlot() < 0 || req.GetSlot() > 9 {
		return nil, status.Errorf(codes.InvalidArgument, "slot must be 0-9, got %d", req.GetSlot())
	}
	if err := s.svc.BindSlot(int(req.GetSlot()), req.GetSessionId()); err != nil {
		return nil, status.Errorf(codes.Internal, "bind: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) UnbindSlot(_ context.Context, req *fleetv1.UnbindSlotRequest) (*emptypb.Empty, error) {
	if req.GetSlot() < 0 || req.GetSlot() > 9 {
		return nil, status.Errorf(codes.InvalidArgument, "slot must be 0-9, got %d", req.GetSlot())
	}
	if err := s.svc.UnbindSlot(int(req.GetSlot())); err != nil {
		return nil, status.Errorf(codes.Internal, "unbind: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// Compile-time check that *Server satisfies FleetServer. The embedded
// UnimplementedFleetServer.mustEmbedUnimplementedFleetServer() satisfies the
// final unexported method.
var _ fleetv1.FleetServer = (*Server)(nil)
