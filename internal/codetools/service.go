package codetools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "eve-beemo/proto/gen/proto"
)

type Service struct {
	pb.UnimplementedCodeToolsServer
	cfg       Config
	resolver  Resolver
	sandbox   Sandbox
	processMu sync.Mutex
	processes map[string]*Process
}

func NewService(cfg Config) *Service {
	return &Service{
		cfg:       cfg,
		resolver:  NewResolver(cfg.Roots),
		sandbox:   Sandbox{MaxOutput: cfg.MaxOutput, Timeout: time.Duration(cfg.CommandTTL) * time.Second},
		processes: map[string]*Process{},
	}
}

func (s *Service) Health(context.Context, *pb.CodeHealthRequest) (*pb.CodeHealthResponse, error) {
	return &pb.CodeHealthResponse{Ready: true, Version: "dev"}, nil
}

func (s *Service) ListWorkspaces(context.Context, *pb.ListWorkspacesRequest) (*pb.ListWorkspacesResponse, error) {
	return &pb.ListWorkspacesResponse{Roots: discoverRepositories(s.resolver.Roots())}, nil
}

func discoverRepositories(roots []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			relative, _ := filepath.Rel(root, path)
			depth := 0
			if relative != "." {
				depth = strings.Count(relative, string(filepath.Separator)) + 1
			}
			if entry.IsDir() && (entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".git") {
				if path != root {
					return filepath.SkipDir
				}
			}
			if entry.IsDir() && depth > 3 {
				return filepath.SkipDir
			}
			if entry.IsDir() {
				if info, statErr := os.Stat(filepath.Join(path, ".git")); statErr == nil && (info.IsDir() || info.Mode().IsRegular()) {
					if !seen[path] {
						seen[path] = true
						result = append(result, path)
					}
					if path != root {
						return filepath.SkipDir
					}
				}
			}
			return nil
		})
	}
	sort.Strings(result)
	return result
}

func (s *Service) Execute(ctx context.Context, req *pb.CodeToolRequest) (*pb.CodeToolResult, error) {
	workspace, err := s.resolver.Workspace(req.GetWorkspace())
	if err != nil {
		return failed(req.GetAction(), err), nil
	}
	args := map[string]any{}
	if raw := strings.TrimSpace(req.GetArgsJson()); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return failed(req.GetAction(), fmt.Errorf("invalid args: %w", err)), nil
		}
	}

	result, err := s.execute(ctx, workspace, strings.TrimSpace(req.GetAction()), args, req.GetApproved())
	if err != nil {
		return failed(req.GetAction(), err), nil
	}
	return result, nil
}

func (s *Service) execute(ctx context.Context, workspace, action string, args map[string]any, approved bool) (*pb.CodeToolResult, error) {
	switch action {
	case "code.list":
		return s.list(workspace, stringArg(args, "path"))
	case "code.read":
		path := stringArg(args, "path")
		if sensitivePath(path) && !approved {
			return &pb.CodeToolResult{Action: action, Status: "approval_required", Output: "reading a potentially sensitive file requires approval", ApprovalId: newID()}, nil
		}
		return s.read(workspace, path, intArg(args, "offset"), intArg(args, "limit"))
	case "code.create":
		path := stringArg(args, "path")
		if sensitivePath(path) && !approved {
			return &pb.CodeToolResult{Action: action, Status: "approval_required", Output: "creating a potentially sensitive file requires approval", ApprovalId: newID()}, nil
		}
		return s.create(workspace, path, stringArgRaw(args, "content"))
	case "code.search":
		return s.search(ctx, workspace, args)
	case "code.files":
		return s.files(ctx, workspace, args)
	case "code.patch":
		patch := stringArg(args, "patch")
		if err := validatePatch(patch); err != nil {
			return nil, err
		}
		if patchTouchesSensitivePath(patch) && !approved {
			return &pb.CodeToolResult{Action: action, Status: "approval_required", Output: "changing a potentially sensitive file requires approval", ApprovalId: newID()}, nil
		}
		return s.run(ctx, workspace, action, "git apply --whitespace=nowarn -", patch, false, true)
	case "code.exec":
		command := stringArg(args, "command")
		if command == "" {
			return nil, fmt.Errorf("command is required")
		}
		reason := approvalReason(command)
		if reason != "" && !approved {
			return &pb.CodeToolResult{Action: action, Status: "approval_required", Output: reason, ApprovalId: newID()}, nil
		}
		return s.run(ctx, workspace, action, command, "", reason != "", false)
	case "code.process_start":
		command := stringArg(args, "command")
		if command == "" {
			return nil, fmt.Errorf("command is required")
		}
		reason := approvalReason(command)
		if reason != "" && !approved {
			return &pb.CodeToolResult{Action: action, Status: "approval_required", Output: reason, ApprovalId: newID()}, nil
		}
		process, err := s.sandbox.Start(workspace, command, reason != "")
		if err != nil {
			return nil, err
		}
		processID := newID()
		s.processMu.Lock()
		s.processes[processID] = process
		s.processMu.Unlock()
		return ok(action, fmt.Sprintf(`{"process_id":%q}`, processID), false), nil
	case "code.process_poll":
		processID := stringArg(args, "process_id")
		process, err := s.process(processID)
		if err != nil {
			return nil, err
		}
		output, running, processErr := process.Poll(s.cfg.MaxOutput)
		body, _ := json.Marshal(map[string]any{"process_id": processID, "running": running, "output": output, "error": errorText(processErr)})
		return ok(action, string(body), false), nil
	case "code.process_input":
		process, err := s.process(stringArg(args, "process_id"))
		if err != nil {
			return nil, err
		}
		if err := process.Write(stringArg(args, "input")); err != nil {
			return nil, err
		}
		return ok(action, "input sent", false), nil
	case "code.process_stop":
		processID := stringArg(args, "process_id")
		process, err := s.process(processID)
		if err != nil {
			return nil, err
		}
		if err := process.Stop(); err != nil {
			return nil, err
		}
		s.processMu.Lock()
		delete(s.processes, processID)
		s.processMu.Unlock()
		return ok(action, "process stopped", false), nil
	case "code.git_status":
		return s.run(ctx, workspace, action, "git status --short", "", false, false)
	case "code.git_diff":
		return s.run(ctx, workspace, action, "git diff --no-ext-diff --", "", false, false)
	default:
		return nil, fmt.Errorf("unsupported code action %q", action)
	}
}

func (s *Service) process(id string) (*Process, error) {
	if id == "" {
		return nil, fmt.Errorf("process_id is required")
	}
	s.processMu.Lock()
	defer s.processMu.Unlock()
	process := s.processes[id]
	if process == nil {
		return nil, fmt.Errorf("unknown process %s", id)
	}
	return process, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) list(workspace, relative string) (*pb.CodeToolResult, error) {
	path, err := s.resolver.ExistingPath(workspace, relative)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	sort.Strings(lines)
	return ok("code.list", strings.Join(lines, "\n"), false), nil
}

func (s *Service) create(workspace, relative, content string) (*pb.CodeToolResult, error) {
	if strings.TrimSpace(relative) == "" {
		return nil, fmt.Errorf("path is required")
	}
	maxBytes := s.cfg.MaxReadBytes
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	if len(content) > maxBytes {
		return nil, fmt.Errorf("content exceeds %d-byte file limit", maxBytes)
	}
	path, err := s.resolver.WritablePath(workspace, relative)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("file already exists; use code.patch to modify it: %s", relative)
		}
		return nil, err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return ok("code.create", relative, true), nil
}

func (s *Service) search(ctx context.Context, workspace string, args map[string]any) (*pb.CodeToolResult, error) {
	query := stringArg(args, "query")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	path := stringArg(args, "path")
	if path == "" {
		path = "."
	}
	parts := []string{"rg", "--line-number", "--no-heading", "--color", "never", "--max-columns", "500", "--max-columns-preview"}
	fixedStrings, err := optionalBoolArg(args, "fixed_strings")
	if err != nil {
		return nil, err
	}
	if fixedStrings {
		parts = append(parts, "--fixed-strings")
	}
	if glob := stringArg(args, "glob"); glob != "" {
		parts = append(parts, "--glob", shellQuote(glob))
	}
	parts = append(parts, "--", shellQuote(query), shellQuote(path))
	output, err := s.runSearchCommand(ctx, workspace, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return ok("code.search", s.limitSearchOutput(output, intArg(args, "max_results")), false), nil
}

func (s *Service) files(ctx context.Context, workspace string, args map[string]any) (*pb.CodeToolResult, error) {
	path := stringArg(args, "path")
	if path == "" {
		path = "."
	}
	parts := []string{"rg", "--files", "--color", "never"}
	if glob := stringArg(args, "glob"); glob != "" {
		parts = append(parts, "--glob", shellQuote(glob))
	}
	parts = append(parts, "--", shellQuote(path))
	output, err := s.runSearchCommand(ctx, workspace, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	if path == "." {
		lines := strings.Split(output, "\n")
		for i := range lines {
			lines[i] = strings.TrimPrefix(lines[i], "./")
		}
		output = strings.Join(lines, "\n")
	}
	if query := strings.ToLower(stringArg(args, "query")); query != "" {
		matches := make([]string, 0)
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(strings.ToLower(line), query) {
				matches = append(matches, line)
			}
		}
		output = strings.Join(matches, "\n")
	}
	return ok("code.files", s.limitSearchOutput(output, intArg(args, "max_results")), false), nil
}

func (s *Service) runSearchCommand(ctx context.Context, workspace, command string) (string, error) {
	// ripgrep uses exit code 1 for a valid search with no matches.
	command += `; status=$?; [ "$status" -eq 0 ] || [ "$status" -eq 1 ]`
	result, err := s.run(ctx, workspace, "search", command, "", false, false)
	if err != nil {
		return "", err
	}
	return result.GetOutput(), nil
}

func (s *Service) limitSearchOutput(output string, requested int) string {
	defaultResults := s.cfg.SearchDefaultResults
	if defaultResults <= 0 {
		defaultResults = 50
	}
	maxResults := s.cfg.SearchMaxResults
	if maxResults <= 0 {
		maxResults = 200
	}
	maxBytes := s.cfg.SearchMaxBytes
	if maxBytes <= 0 {
		maxBytes = 16 * 1024
	}
	if requested <= 0 {
		requested = defaultResults
	}
	requested = min(requested, maxResults)
	return limitResultLines(strings.TrimSpace(output), requested, maxBytes)
}

func limitResultLines(output string, maxLines, maxBytes int) string {
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, min(len(lines), maxLines))
	used := 0
	for _, line := range lines {
		additional := len(line)
		if len(kept) > 0 {
			additional++
		}
		if len(kept) >= maxLines || used+additional > maxBytes {
			break
		}
		kept = append(kept, line)
		used += additional
	}
	result := strings.Join(kept, "\n")
	if omitted := len(lines) - len(kept); omitted > 0 {
		result += fmt.Sprintf("\n[truncated: %d additional results]", omitted)
	}
	return result
}

func (s *Service) read(workspace, relative string, offset, limit int) (*pb.CodeToolResult, error) {
	if strings.TrimSpace(relative) == "" {
		return nil, fmt.Errorf("path is required")
	}
	path, err := s.resolver.ExistingPath(workspace, relative)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file")
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > s.cfg.MaxReadBytes {
		limit = s.cfg.MaxReadBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(int64(offset), 0); err != nil {
		return nil, err
	}
	buffer := make([]byte, limit+1)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return nil, err
	}
	output := string(buffer[:min(n, limit)])
	if n > limit {
		output += "\n[file truncated]"
	}
	return ok("code.read", output, false), nil
}

func (s *Service) run(ctx context.Context, workspace, action, command, stdin string, network, changed bool) (*pb.CodeToolResult, error) {
	output, err := s.sandbox.Run(ctx, workspace, command, stdin, network)
	if err != nil {
		if strings.TrimSpace(output) != "" {
			return nil, fmt.Errorf("%w\n%s", err, output)
		}
		return nil, err
	}
	return ok(action, output, changed), nil
}

func ok(action, output string, changed bool) *pb.CodeToolResult {
	return &pb.CodeToolResult{Action: action, Output: output, Status: "ok", Changed: changed}
}

func failed(action string, err error) *pb.CodeToolResult {
	return &pb.CodeToolResult{Action: action, Status: "error", Error: err.Error()}
}

func stringArg(args map[string]any, name string) string {
	value, ok := args[name]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func stringArgRaw(args map[string]any, name string) string {
	value, ok := args[name]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func intArg(args map[string]any, name string) int {
	value := stringArg(args, name)
	n, _ := strconv.Atoi(value)
	return n
}

func optionalBoolArg(args map[string]any, name string) (bool, error) {
	value, ok := args[name]
	if !ok || value == nil {
		return false, nil
	}
	result, err := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return result, nil
}

func validatePatch(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return fmt.Errorf("patch is required")
	}
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "+++ "))
			name = strings.TrimPrefix(name, "a/")
			name = strings.TrimPrefix(name, "b/")
			if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
				return fmt.Errorf("patch path escapes workspace: %s", name)
			}
		}
	}
	return nil
}

func patchTouchesSensitivePath(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "+++ "))
			name = strings.TrimPrefix(strings.TrimPrefix(name, "a/"), "b/")
			if sensitivePath(name) {
				return true
			}
		}
	}
	return false
}

func sensitivePath(path string) bool {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
	if name == ".env.example" || name == ".env.sample" {
		return false
	}
	return name == ".env" || strings.HasPrefix(name, ".env.") ||
		strings.Contains(name, "credential") || strings.Contains(name, "secret") ||
		strings.HasPrefix(name, "id_rsa") || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key")
}

func approvalReason(command string) string {
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(command), " ")) + " "
	patterns := []struct{ needle, reason string }{
		{" .env ", "reading a potentially sensitive file requires approval"},
		{" rm ", "destructive file removal requires approval"},
		{" git reset ", "git reset requires approval"},
		{" git clean ", "git clean requires approval"},
		{" git checkout ", "git checkout requires approval"},
		{" git commit ", "git commit requires approval"},
		{" git push ", "git push requires approval"},
		{" git pull ", "network access requires approval"},
		{" git fetch ", "network access requires approval"},
		{" curl ", "network access requires approval"},
		{" wget ", "network access requires approval"},
		{" npm install ", "dependency installation requires approval"},
		{" npm i ", "dependency installation requires approval"},
		{" npm add ", "dependency installation requires approval"},
		{" pnpm install ", "dependency installation requires approval"},
		{" pnpm i ", "dependency installation requires approval"},
		{" pnpm add ", "dependency installation requires approval"},
		{" yarn add ", "dependency installation requires approval"},
		{" go get ", "dependency installation requires approval"},
		{" go mod download ", "dependency installation requires approval"},
		{" cargo add ", "dependency installation requires approval"},
		{" cargo fetch ", "dependency installation requires approval"},
		{" pip install ", "dependency installation requires approval"},
		{" python -m pip install ", "dependency installation requires approval"},
		{" uv add ", "dependency installation requires approval"},
		{" sudo ", "privileged host commands are not allowed without approval"},
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern.needle) {
			return pattern.reason
		}
	}
	return ""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func newID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("approval-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
