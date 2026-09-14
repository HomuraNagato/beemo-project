package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	pb "eve-beemo/proto/gen/proto"
	orchdb "eve-beemo/src/orchestrator/db"
)

type agentControl struct {
	action string
	target string
}

func parseAgentControl(input string) (agentControl, bool) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	if lower == "/code" {
		return agentControl{action: "code"}, true
	}
	if lower == "/chat" {
		return agentControl{action: "chat"}, true
	}
	if lower == "/switch" {
		return agentControl{action: "switch"}, true
	}
	if lower == "/internet" {
		return agentControl{action: "internet"}, true
	}
	if strings.HasPrefix(lower, "/switch ") {
		return agentControl{action: "switch", target: strings.TrimSpace(trimmed[len("/switch "):])}, true
	}

	normalized := strings.TrimSpace(strings.TrimRightFunc(lower, func(r rune) bool {
		return unicode.IsPunct(r) && r != '/' && r != '-' && r != '_'
	}))
	switch normalized {
	case "enter code mode", "enter programming mode", "start code mode", "start programming mode":
		return agentControl{action: "code"}, true
	case "enter chat mode", "switch to chat mode", "exit code mode", "leave code mode", "exit programming mode":
		return agentControl{action: "chat"}, true
	case "switch projects", "switch project", "change projects", "change project", "switch repositories", "switch repository":
		return agentControl{action: "switch"}, true
	}
	for _, prefix := range []string{"switch to project ", "switch to repository ", "switch to repo ", "switch to "} {
		if strings.HasPrefix(normalized, prefix) && strings.TrimSpace(trimmed[len(prefix):]) != "" {
			return agentControl{action: "switch", target: strings.TrimSpace(trimmed[len(prefix):])}, true
		}
	}
	return agentControl{}, false
}

func (s *orchestratorServer) resolveAgentState(ctx context.Context, req *pb.AgentRequest) (string, string) {
	mode := strings.ToLower(strings.TrimSpace(req.GetMode()))
	workspace := strings.TrimSpace(req.GetWorkspace())
	if s.agentStore != nil && strings.TrimSpace(req.GetSessionId()) != "" {
		if session, _, err := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, req.GetSessionId()); err == nil {
			if mode == "" {
				mode = session.Mode
			}
			if workspace == "" && mode == "code" {
				workspace = session.Workspace
			}
		}
	}
	if mode == "" {
		mode = "chat"
	}
	if mode == "chat" {
		workspace = ""
	}
	if mode == "code" && s.agentStore != nil {
		if _, ok, err := s.registeredProject(ctx, workspace); err != nil || !ok {
			mode, workspace = "chat", ""
		}
	}
	return mode, workspace
}

func (s *orchestratorServer) runAgentControl(ctx context.Context, req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer, command agentControl) error {
	if s.agentStore == nil {
		return s.controlResponse(ctx, req, stream, "control_error", "Session controls are unavailable.", map[string]any{})
	}
	switch command.action {
	case "chat":
		session, err := s.agentStore.SetSessionMode(ctx, s.cfg.MemoryUserKey, req.GetSessionId(), "chat", "")
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		req.Mode, req.Workspace = session.Mode, session.Workspace
		return s.controlResponse(ctx, req, stream, "mode_changed", "Chat mode.", map[string]any{"mode": "chat", "workspace": ""})
	case "code":
		project, ok, err := s.projectForCodeMode(ctx, req.GetWorkspace())
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		if !ok {
			return s.controlResponse(ctx, req, stream, "project_manager", "Add a code repository first.", map[string]any{"mode": "chat"})
		}
		if _, err := s.agentStore.UseProject(ctx, s.cfg.MemoryUserKey, project.Path); err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		session, err := s.agentStore.SetSessionMode(ctx, s.cfg.MemoryUserKey, req.GetSessionId(), "code", project.Path)
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		req.Mode, req.Workspace = session.Mode, session.Workspace
		return s.controlResponse(ctx, req, stream, "mode_changed", fmt.Sprintf("Code mode: %s.", project.Name), map[string]any{"mode": "code", "workspace": project.Path})
	case "switch":
		projects, err := s.agentStore.ListProjects(ctx, s.cfg.MemoryUserKey)
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		if strings.TrimSpace(command.target) == "" {
			return s.projectPickerResponse(ctx, req, stream, projects)
		}
		project, matches := matchProject(projects, command.target)
		if matches != 1 {
			message := "No registered project matches that name."
			if matches > 1 {
				message = "That project name is ambiguous. Choose a project."
			}
			return s.controlResponse(ctx, req, stream, "project_picker", message, map[string]any{"projects": projectPayload(projects)})
		}
		if _, err := s.agentStore.UseProject(ctx, s.cfg.MemoryUserKey, project.Path); err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		if _, err := s.agentStore.SetSessionMode(ctx, s.cfg.MemoryUserKey, req.GetSessionId(), "code", project.Path); err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		req.Mode, req.Workspace = "code", project.Path
		return s.controlResponse(ctx, req, stream, "mode_changed", fmt.Sprintf("Code mode: %s.", project.Name), map[string]any{"mode": "code", "workspace": project.Path})
	case "internet":
		session, _, err := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, req.GetSessionId())
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		enabled := !session.InternetEnabled
		if enabled && strings.TrimSpace(s.cfg.InternetAPIKey) == "" {
			return s.controlResponse(ctx, req, stream, "internet_unavailable", "Internet search is not configured. Add BRAVE_SEARCH_API_KEY to Beemo's environment.", map[string]any{"internetEnabled": false})
		}
		session, err = s.agentStore.SetSessionInternet(ctx, s.cfg.MemoryUserKey, req.GetSessionId(), enabled)
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		if req.Options == nil {
			req.Options = map[string]string{}
		}
		req.Options["internet_enabled"] = fmt.Sprintf("%t", session.InternetEnabled)
		message := "Internet access disabled."
		if session.InternetEnabled {
			message = "Internet access enabled."
		}
		return s.controlResponse(ctx, req, stream, "internet_changed", message, map[string]any{"internetEnabled": session.InternetEnabled})
	}
	return nil
}

func (s *orchestratorServer) projectForCodeMode(ctx context.Context, current string) (orchdb.AgentProject, bool, error) {
	projects, err := s.agentStore.ListProjects(ctx, s.cfg.MemoryUserKey)
	if err != nil {
		return orchdb.AgentProject{}, false, err
	}
	for _, project := range projects {
		if project.Path == current {
			return project, true, nil
		}
	}
	if len(projects) == 0 {
		return orchdb.AgentProject{}, false, nil
	}
	return projects[0], true, nil
}

func (s *orchestratorServer) projectPickerResponse(ctx context.Context, req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer, projects []orchdb.AgentProject) error {
	if len(projects) == 0 {
		return s.controlResponse(ctx, req, stream, "project_manager", "Add a code repository first.", map[string]any{})
	}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.Name)
	}
	return s.controlResponse(ctx, req, stream, "project_picker", "Choose a project: "+strings.Join(names, ", ")+".", map[string]any{"projects": projectPayload(projects)})
}

func (s *orchestratorServer) controlResponse(ctx context.Context, req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer, action, message string, payload map[string]any) error {
	payload["action"] = action
	if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "control", "", message, payload, ""); err != nil {
		return err
	}
	if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "assistant", "", message, nil, ""); err != nil {
		return err
	}
	return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "complete", "", "complete", nil, "")
}

func matchProject(projects []orchdb.AgentProject, target string) (orchdb.AgentProject, int) {
	normalizedTarget := normalizeProjectName(target)
	var matched orchdb.AgentProject
	matches := 0
	for _, project := range projects {
		if project.Path == strings.TrimSpace(target) || normalizeProjectName(project.Name) == normalizedTarget {
			matched, matches = project, matches+1
		}
	}
	return matched, matches
}

func normalizeProjectName(value string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(strings.TrimSpace(value)))), " ")
}

func projectPayload(projects []orchdb.AgentProject) []map[string]any {
	result := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		result = append(result, map[string]any{"name": project.Name, "path": project.Path, "isGit": project.IsGit})
	}
	return result
}
