package lifecycle

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type UpOptions struct {
	Build   bool
	Memory  bool
	UI      bool
	Voice   bool
	Code    bool
	Timeout time.Duration
}

type Manager struct {
	Paths    Paths
	Profile  Profile
	Runner   Runner
	Output   io.Writer
	Client   *http.Client
	WaitCode func(context.Context, string) error
}

func (m Manager) Init(ctx context.Context, accelerator string, models, force bool) error {
	accelerator = strings.ToLower(strings.TrimSpace(accelerator))
	if accelerator != "gpu" && accelerator != "cpu" {
		return fmt.Errorf("invalid accelerator %q; expected gpu or cpu", accelerator)
	}
	args := []string{accelerator}
	if !models {
		args = append(args, "--no-models")
	}
	if force {
		args = append(args, "--force-download")
	}
	return m.Runner.Run(ctx, m.Paths.BeemoRoot, "./scripts/beemo-init.sh", args...)
}

type serviceCheck struct {
	Name      string
	Container string
	URL       string
}

func (m Manager) Up(ctx context.Context, options UpOptions) error {
	sessionID, err := RotateSession(m.Paths)
	if err != nil {
		return err
	}
	m.printf("session: %s\n", sessionID)
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Minute
	}
	if m.Profile.LocalDB {
		if err := m.composeUp(ctx, []string{"eve-db"}, false); err != nil {
			return err
		}
		if err := m.waitPostgres(ctx, options.Timeout); err != nil {
			return err
		}
	}
	if options.Code {
		if err := m.startCode(ctx, options.Build, false); err != nil {
			return err
		}
	}
	if err := m.composeUp(ctx, []string{"eve-reasoning", "eve-embedding", "eve-reranker"}, options.Build); err != nil {
		return err
	}
	for _, check := range []serviceCheck{
		{Name: "reasoning", Container: "eve-reasoning", URL: "http://127.0.0.1:5014/health"},
		{Name: "embedding", Container: "eve-embedding", URL: "http://127.0.0.1:5021/health"},
		{Name: "reranker", Container: "eve-reranker", URL: "http://127.0.0.1:5022/health"},
	} {
		if err := m.waitHTTP(ctx, check, options.Timeout); err != nil {
			return err
		}
	}

	if options.Memory {
		if err := m.memoryCompose(ctx, "up", "-d", buildArg(options.Build), "memory_palace"); err != nil {
			return err
		}
		if err := m.waitHTTP(ctx, serviceCheck{Name: "memory", Container: "memory_palace", URL: "http://127.0.0.1:8013/health"}, options.Timeout); err != nil {
			return err
		}
	}

	if err := m.composeUp(ctx, []string{"eve-orchestrator"}, options.Build); err != nil {
		return err
	}
	if err := m.waitOrchestrator(ctx, options.Timeout); err != nil {
		return err
	}
	if options.UI {
		if err := m.composeUp(ctx, []string{"eve-ui"}, options.Build); err != nil {
			return err
		}
		if err := m.waitHTTP(ctx, serviceCheck{Name: "ui", Container: "eve-ui", URL: "http://127.0.0.1:5017/healthz"}, options.Timeout); err != nil {
			return err
		}
	}
	if options.Voice {
		if err := m.composeUp(ctx, []string{"eve-asr", "eve-wakeword"}, options.Build); err != nil {
			return err
		}
	}
	m.printf("Beemo is ready (%s)\n", m.Profile.Name)
	return nil
}

func (m Manager) Down(ctx context.Context, memory, ui, voice bool) error {
	services := []string{}
	if voice {
		services = append(services, "eve-wakeword", "eve-asr")
	}
	if ui {
		services = append(services, "eve-ui")
	}
	services = append(services, "eve-orchestrator")
	if err := m.compose(ctx, append([]string{"stop"}, services...)...); err != nil {
		return err
	}
	if memory {
		if err := m.memoryCompose(ctx, "stop", "memory_palace"); err != nil {
			return err
		}
	}
	if err := m.compose(ctx, "stop", "eve-reranker", "eve-embedding", "eve-reasoning"); err != nil {
		return err
	}
	if m.Profile.LocalDB {
		if err := m.compose(ctx, "stop", "eve-db"); err != nil {
			return err
		}
	}
	if _, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "systemctl", "--user", "is-active", "beemo-code.service"); err == nil {
		return m.Runner.Run(ctx, m.Paths.BeemoRoot, "systemctl", "--user", "stop", "beemo-code.service")
	}
	return nil
}

func (m Manager) Status(ctx context.Context) error {
	checks := []serviceCheck{
		{Name: "eve-reasoning", URL: "http://127.0.0.1:5014/health"},
		{Name: "eve-embedding", URL: "http://127.0.0.1:5021/health"},
		{Name: "eve-reranker", URL: "http://127.0.0.1:5022/health"},
		{Name: "memory_palace", URL: "http://127.0.0.1:8013/health"},
		{Name: "eve-ui", URL: "http://127.0.0.1:5017/healthz"},
	}
	containerNames := []string{"eve-db", "eve-reasoning", "eve-embedding", "eve-reranker", "memory_palace", "eve-ui", "eve-orchestrator", "eve-asr", "eve-wakeword"}
	states := m.containerStates(ctx, containerNames)
	m.printf("profile: %s\n", m.Profile.Name)
	if sessionID, err := ReadSession(m.Paths); err == nil {
		m.printf("session: %s\n", sessionID)
	}
	codeState := "stopped"
	if output, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "systemctl", "--user", "is-active", "beemo-code.service"); err == nil {
		codeState = strings.TrimSpace(output)
	}
	m.printf("%-18s %-12s %s\n", "beemo-code", codeState, "local")
	if m.Profile.LocalDB {
		m.printf("%-18s %-12s %s\n", "eve-db", states["eve-db"], "local")
	}
	for _, check := range checks {
		state := states[check.Name]
		health := "-"
		if state == "running" {
			if m.httpReady(ctx, check.URL) {
				health = "ready"
			} else {
				health = "not-ready"
			}
		}
		m.printf("%-18s %-12s %s\n", check.Name, state, health)
	}
	orchestratorState := states["eve-orchestrator"]
	orchestratorHealth := "-"
	if orchestratorState == "running" {
		if m.orchestratorReady(ctx) {
			orchestratorHealth = "ready"
		} else {
			orchestratorHealth = "not-ready"
		}
	}
	m.printf("%-18s %-12s %s\n", "eve-orchestrator", orchestratorState, orchestratorHealth)
	for _, name := range []string{"eve-asr", "eve-wakeword"} {
		m.printf("%-18s %-12s %s\n", name, states[name], "-")
	}
	return nil
}

func (m Manager) Doctor(ctx context.Context) error {
	checks := []struct {
		label string
		fn    func() error
	}{
		{"Docker", func() error { _, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "docker", "version"); return err }},
		{"Beemo Compose", func() error { return m.compose(ctx, "config", "--quiet") }},
		{"Memory Palace Compose", func() error { return m.memoryCompose(ctx, "config", "--quiet") }},
		{"Bubblewrap", func() error { _, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "bwrap", "--version"); return err }},
		{"User systemd", func() error {
			_, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "systemctl", "--user", "show-environment")
			return err
		}},
	}
	failed := false
	for _, check := range checks {
		if err := check.fn(); err != nil {
			m.printf("fail %s: %v\n", check.label, err)
			failed = true
		} else {
			m.printf("ok   %s\n", check.label)
		}
	}
	if failed {
		return fmt.Errorf("doctor found configuration errors")
	}
	return nil
}

func (m Manager) Logs(ctx context.Context, service string, tail int, follow bool) error {
	if service == "beemo-code" || service == "code" {
		args := []string{"--user", "-u", "beemo-code.service"}
		if tail > 0 {
			args = append(args, "-n", strconv.Itoa(tail))
		}
		if follow {
			args = append(args, "-f")
		}
		return m.Runner.Run(ctx, m.Paths.BeemoRoot, "journalctl", args...)
	}
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	if tail > 0 {
		args = append(args, "--tail", strconv.Itoa(tail))
	}
	args = append(args, service)
	return m.Runner.Run(ctx, m.Paths.BeemoRoot, "docker", args...)
}

func (m Manager) Restart(ctx context.Context, service string, build bool) error {
	service = strings.TrimSpace(service)
	if service == "" {
		return fmt.Errorf("service is required")
	}
	sessionID, err := RotateSession(m.Paths)
	if err != nil {
		return err
	}
	m.printf("session: %s\n", sessionID)
	if service == "memory" || service == "memory_palace" {
		return m.memoryCompose(ctx, "up", "-d", buildArg(build), "--force-recreate", "memory_palace")
	}
	if service == "code" || service == "beemo-code" {
		return m.startCode(ctx, build, true)
	}
	args := []string{"up", "-d"}
	if build {
		args = append(args, "--build")
	}
	args = append(args, "--force-recreate", service)
	return m.compose(ctx, args...)
}

func (m Manager) startCode(ctx context.Context, build, forceRestart bool) error {
	binary := filepath.Join(m.Paths.BeemoRoot, "bin", "beemo-code")
	if build {
		if err := m.Runner.Run(ctx, m.Paths.BeemoRoot, "go", "build", "-o", binary, "./cmd/beemo-code"); err != nil {
			return err
		}
	}
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("beemo-code binary missing; run beemo up --build or make build: %w", err)
	}
	socket := codeSocketPath()
	if _, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "systemctl", "--user", "is-active", "beemo-code.service"); err == nil {
		if !build && !forceRestart {
			if info, statErr := os.Stat(socket); statErr == nil && info.Mode()&os.ModeSocket != 0 {
				return nil
			}
		}
		if err := m.Runner.Run(ctx, m.Paths.BeemoRoot, "systemctl", "--user", "stop", "beemo-code.service"); err != nil {
			return err
		}
	}
	roots := strings.TrimSpace(os.Getenv("BEEMO_CODE_ROOTS"))
	if roots == "" {
		roots = filepath.Dir(m.Paths.BeemoRoot)
	}
	if err := m.Runner.Run(ctx, m.Paths.BeemoRoot,
		"systemd-run", "--user", "--unit=beemo-code", "--collect",
		"--property=Restart=on-failure", "--property=RestartSec=2",
		"--working-directory="+m.Paths.BeemoRoot,
		"--setenv=BEEMO_CODE_ROOTS="+roots,
		"--setenv=BEEMO_CODE_SOCKET="+socket,
		binary,
	); err != nil {
		return err
	}
	return m.waitCode(ctx, socket)
}

func (m Manager) waitCode(ctx context.Context, path string) error {
	if m.WaitCode != nil {
		return m.WaitCode(ctx, path)
	}
	return waitForSocket(ctx, path)
}

func codeSocketPath() string {
	if socket := strings.TrimSpace(os.Getenv("BEEMO_CODE_SOCKET")); socket != "" {
		return socket
	}
	runtimeDir := strings.TrimSpace(os.Getenv("BEEMO_CODE_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = "/tmp/beemo-code"
	}
	return filepath.Join(runtimeDir, "beemo-code.sock")
}

func waitForSocket(ctx context.Context, path string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for beemo-code socket %s", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (m Manager) composeUp(ctx context.Context, services []string, build bool) error {
	args := []string{"up", "-d"}
	if build {
		args = append(args, "--build")
	}
	args = append(args, services...)
	return m.compose(ctx, args...)
}

func (m Manager) compose(ctx context.Context, args ...string) error {
	command := append(m.Profile.ComposeArgs(), args...)
	return m.Runner.Run(ctx, m.Paths.BeemoRoot, "docker", command...)
}

func (m Manager) memoryCompose(ctx context.Context, args ...string) error {
	filtered := args[:0]
	for _, arg := range args {
		if arg != "" {
			filtered = append(filtered, arg)
		}
	}
	command := append([]string{"compose", "-f", "docker-compose.yaml"}, filtered...)
	return m.Runner.Run(ctx, m.Paths.MemoryPalaceRoot, "docker", command...)
}

func (m Manager) waitHTTP(ctx context.Context, check serviceCheck, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if m.httpReady(ctx, check.URL) {
			m.printf("%-18s ready\n", check.Name)
			return nil
		}
		if check.Container != "" {
			state := m.containerState(ctx, check.Container)
			if state == "exited" || state == "dead" {
				return fmt.Errorf("%s container stopped before becoming ready", check.Container)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s at %s", check.Name, check.URL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (m Manager) waitOrchestrator(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if m.orchestratorReady(ctx) {
			m.printf("%-18s ready\n", "orchestrator")
			return nil
		}
		state := m.containerState(ctx, "eve-orchestrator")
		if state == "exited" || state == "dead" {
			return fmt.Errorf("eve-orchestrator stopped before becoming ready")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for orchestrator gRPC health")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (m Manager) waitPostgres(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "docker", "exec", "eve-db", "pg_isready", "-U", "postgres", "-d", "beemo"); err == nil {
			m.printf("%-18s ready\n", "database")
			return nil
		}
		state := m.containerState(ctx, "eve-db")
		if state == "exited" || state == "dead" {
			return fmt.Errorf("eve-db stopped before becoming ready")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for PostgreSQL readiness")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m Manager) orchestratorReady(ctx context.Context) bool {
	_, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "docker", "exec", "eve-orchestrator", "grpcurl", "-plaintext", "-d", `{"service":"eve.Orchestrator"}`, "localhost:5013", "grpc.health.v1.Health/Check")
	return err == nil
}

func (m Manager) httpReady(ctx context.Context, url string) bool {
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func (m Manager) containerState(ctx context.Context, name string) string {
	output, err := m.Runner.Output(ctx, m.Paths.BeemoRoot, "docker", "inspect", "--format", "{{.State.Status}}", name)
	if err != nil || strings.TrimSpace(output) == "" {
		return "absent"
	}
	return strings.TrimSpace(output)
}

func (m Manager) containerStates(ctx context.Context, names []string) map[string]string {
	states := make(map[string]string, len(names))
	for _, name := range names {
		states[name] = "absent"
	}
	args := append([]string{"inspect", "--format", "{{.Name}}|{{.State.Status}}"}, names...)
	output, _ := m.Runner.Output(ctx, m.Paths.BeemoRoot, "docker", args...)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 2)
		if len(parts) == 2 {
			states[strings.TrimPrefix(parts[0], "/")] = parts[1]
		}
	}
	return states
}

func (m Manager) printf(format string, args ...any) {
	if m.Output != nil {
		fmt.Fprintf(m.Output, format, args...)
	}
}

func buildArg(enabled bool) string {
	if enabled {
		return "--build"
	}
	return ""
}
