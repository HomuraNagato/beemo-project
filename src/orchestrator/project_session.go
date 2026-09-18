package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"unicode/utf8"

	pb "eve-beemo/proto/gen/proto"
	orchdb "eve-beemo/src/orchestrator/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *orchestratorServer) BrowseAgentDirectories(ctx context.Context, req *pb.BrowseDirectoriesRequest) (*pb.BrowseDirectoriesResponse, error) {
	if s.codeTools == nil {
		return nil, status.Error(codes.Unavailable, "beemo-code is unavailable")
	}
	return s.codeTools.BrowseDirectories(ctx, req)
}

func (s *orchestratorServer) ListAgentProjects(ctx context.Context, _ *pb.ProjectListRequest) (*pb.ProjectListResponse, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "project storage is unavailable")
	}
	projects, err := s.agentStore.ListProjects(ctx, s.cfg.MemoryUserKey)
	if err != nil {
		return nil, err
	}
	response := &pb.ProjectListResponse{Projects: make([]*pb.AgentProject, 0, len(projects))}
	for _, project := range projects {
		response.Projects = append(response.Projects, projectProto(project))
	}
	return response, nil
}

func (s *orchestratorServer) AddAgentProject(ctx context.Context, req *pb.ProjectRequest) (*pb.AgentProject, error) {
	if s.agentStore == nil || s.codeTools == nil {
		return nil, status.Error(codes.Unavailable, "project management is unavailable")
	}
	browse, err := s.codeTools.BrowseDirectories(ctx, &pb.BrowseDirectoriesRequest{Path: strings.TrimSpace(req.GetPath())})
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid project directory: %v", err)
	}
	path := strings.TrimSpace(browse.GetPath())
	if path == "" {
		return nil, status.Error(codes.InvalidArgument, "project path is required")
	}
	project, err := s.agentStore.AddProject(ctx, s.cfg.MemoryUserKey, path, filepath.Base(path), browse.GetIsGit())
	if err != nil {
		return nil, err
	}
	return projectProto(project), nil
}

func (s *orchestratorServer) RemoveAgentProject(ctx context.Context, req *pb.ProjectRequest) (*pb.ProjectMutationResponse, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "project storage is unavailable")
	}
	path := strings.TrimSpace(req.GetPath())
	removed, err := s.agentStore.RemoveProject(ctx, s.cfg.MemoryUserKey, path)
	if err != nil {
		return nil, err
	}
	response := &pb.ProjectMutationResponse{Removed: removed}
	if sessionID := strings.TrimSpace(req.GetSessionId()); removed && sessionID != "" {
		session, _, getErr := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, sessionID)
		if getErr == nil {
			if session.Workspace == path {
				session, err = s.agentStore.SetSessionMode(ctx, s.cfg.MemoryUserKey, sessionID, "normal", "")
				if err != nil {
					return nil, err
				}
			}
			response.Mode, response.Workspace = session.Mode, session.Workspace
		}
	}
	return response, nil
}

func (s *orchestratorServer) SelectAgentProject(ctx context.Context, req *pb.ProjectSelectionRequest) (*pb.AgentSessionSummary, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "project storage is unavailable")
	}
	path := strings.TrimSpace(req.GetPath())
	if _, err := s.agentStore.UseProject(ctx, s.cfg.MemoryUserKey, path); err != nil {
		if err == sql.ErrNoRows {
			return nil, status.Error(codes.NotFound, "project is not registered")
		}
		return nil, err
	}
	session, err := s.agentStore.SetSessionMode(ctx, s.cfg.MemoryUserKey, strings.TrimSpace(req.GetSessionId()), "code", path)
	if err != nil {
		return nil, err
	}
	s.publishControl(session.SessionID, "mode_changed", "Code mode", map[string]any{"mode": "code", "workspace": path})
	return sessionProto(session), nil
}

func (s *orchestratorServer) CreateAgentSession(ctx context.Context, req *pb.SessionCreateRequest) (*pb.AgentSessionSummary, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "session storage is unavailable")
	}
	sessionID := strings.TrimSpace(req.GetSessionId())
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "session id is required")
	}
	session, err := s.agentStore.CreateSession(ctx, sessionID, s.cfg.MemoryUserKey)
	if err != nil {
		return nil, err
	}
	return sessionProto(session), nil
}

func (s *orchestratorServer) ActivateAgentSession(ctx context.Context, req *pb.SessionRequest) (*pb.AgentSessionSummary, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "session storage is unavailable")
	}
	session, _, err := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, strings.TrimSpace(req.GetSessionId()))
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if err != nil {
		return nil, err
	}
	if session.Mode == "code" {
		if _, ok, projectErr := s.registeredProject(ctx, session.Workspace); projectErr != nil {
			return nil, projectErr
		} else if !ok {
			session, err = s.agentStore.SetSessionMode(ctx, s.cfg.MemoryUserKey, session.SessionID, "normal", "")
			if err != nil {
				return nil, err
			}
		}
	}
	return sessionProto(session), nil
}

func (s *orchestratorServer) RenameAgentSession(ctx context.Context, req *pb.SessionRenameRequest) (*pb.AgentSessionSummary, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "session storage is unavailable")
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return nil, status.Error(codes.InvalidArgument, "session name must be between 1 and 80 characters")
	}
	session, err := s.agentStore.RenameSession(ctx, s.cfg.MemoryUserKey, strings.TrimSpace(req.GetSessionId()), name)
	if err == sql.ErrNoRows {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if err != nil {
		return nil, err
	}
	return sessionProto(session), nil
}

func (s *orchestratorServer) DeleteAgentSessions(ctx context.Context, req *pb.SessionDeleteRequest) (*pb.SessionDeleteResponse, error) {
	if s.agentStore == nil {
		return nil, status.Error(codes.Unavailable, "session storage is unavailable")
	}
	active := strings.TrimSpace(req.GetActiveSessionId())
	ids := make([]string, 0, len(req.GetSessionIds()))
	seen := map[string]bool{}
	for _, raw := range req.GetSessionIds() {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		if id == active {
			return nil, status.Error(codes.FailedPrecondition, "the active session cannot be deleted")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return &pb.SessionDeleteResponse{}, nil
	}
	deleted, err := s.agentStore.DeleteSessions(ctx, s.cfg.MemoryUserKey, ids)
	if err != nil {
		return nil, err
	}
	return &pb.SessionDeleteResponse{Deleted: int32(deleted)}, nil
}

func projectProto(project orchdb.AgentProject) *pb.AgentProject {
	result := &pb.AgentProject{
		Path: project.Path, Name: project.Name, IsGit: project.IsGit,
		CreatedUnixMs: project.CreatedAt.UnixMilli(),
	}
	if project.LastUsedAt.Valid {
		result.LastUsedUnixMs = project.LastUsedAt.Time.UnixMilli()
	}
	return result
}

func sessionProto(session orchdb.AgentSession) *pb.AgentSessionSummary {
	return &pb.AgentSessionSummary{
		SessionId: session.SessionID, Name: session.Name, Mode: session.Mode, Workspace: session.Workspace,
		Status: session.Status, UpdatedUnixMs: session.UpdatedAt.UnixMilli(), InternetEnabled: session.InternetEnabled,
	}
}

func (s *orchestratorServer) registeredProject(ctx context.Context, path string) (orchdb.AgentProject, bool, error) {
	projects, err := s.agentStore.ListProjects(ctx, s.cfg.MemoryUserKey)
	if err != nil {
		return orchdb.AgentProject{}, false, err
	}
	for _, project := range projects {
		if project.Path == path {
			return project, true, nil
		}
	}
	return orchdb.AgentProject{}, false, nil
}

func (s *orchestratorServer) publishControl(sessionID, action, message string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["action"] = action
	s.publishStatePayload(sessionID, "control", message, payload)
}
