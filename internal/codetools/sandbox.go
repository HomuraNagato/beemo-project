package codetools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Sandbox struct {
	MaxOutput int
	Timeout   time.Duration
}

type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *safeBuffer
	done   chan error
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *safeBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (s Sandbox) Run(ctx context.Context, workspace, command, stdin string, network bool) (string, error) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return "", fmt.Errorf("local sandbox unavailable: bwrap not found")
	}
	if s.Timeout <= 0 {
		s.Timeout = 2 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	args, err := sandboxArgs(workspace, command, network)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(commandCtx, "bwrap", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = strings.NewReader(stdin)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	if commandCtx.Err() != nil {
		return truncate(output.String(), s.MaxOutput), fmt.Errorf("command timed out: %w", commandCtx.Err())
	}
	text := truncate(output.String(), s.MaxOutput)
	if err != nil {
		return text, fmt.Errorf("sandbox command failed: %w", err)
	}
	return text, nil
}

func (s Sandbox) Start(workspace, command string, network bool) (*Process, error) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return nil, fmt.Errorf("local sandbox unavailable: bwrap not found")
	}
	args, err := sandboxArgs(workspace, command, network)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("bwrap", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output := &safeBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	process := &Process{cmd: cmd, stdin: stdin, output: output, done: make(chan error, 1)}
	go func() { process.done <- cmd.Wait() }()
	return process, nil
}

func (p *Process) Write(input string) error {
	if p == nil || p.stdin == nil {
		return fmt.Errorf("process input is unavailable")
	}
	_, err := io.WriteString(p.stdin, input)
	return err
}

func (p *Process) Poll(maxOutput int) (string, bool, error) {
	if p == nil {
		return "", false, fmt.Errorf("process is unavailable")
	}
	select {
	case err := <-p.done:
		p.done <- err
		return truncate(p.output.String(), maxOutput), false, err
	default:
		return truncate(p.output.String(), maxOutput), true, nil
	}
}

func (p *Process) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(2 * time.Second):
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
}

func sandboxArgs(workspace, command string, network bool) ([]string, error) {
	current, err := user.Current()
	if err != nil {
		return nil, err
	}
	home := current.HomeDir
	args := []string{
		"--die-with-parent", "--new-session", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp",
		"--tmpfs", home,
	}
	for _, maskRoot := range []string{"/exos", "/mnt", "/media"} {
		if _, statErr := os.Stat(maskRoot); statErr != nil {
			continue
		}
		args = append(args, "--tmpfs", maskRoot)
		if within(maskRoot, workspace) {
			for _, parent := range mountParents(maskRoot, workspace) {
				args = append(args, "--dir", parent)
			}
		}
	}
	if !network {
		args = append(args, "--unshare-net")
	}

	readOnly := existingPaths(home, []string{".nvm", ".rustup", ".local/bin", "go/bin", ".cargo/bin"})
	writable := existingPaths(home, []string{
		".cache/go-build", "go/pkg/mod", ".npm", ".local/share/pnpm/store",
		".gradle/caches", ".gradle/wrapper", ".cargo/registry", ".cargo/git",
	})
	for _, path := range appendParents(home, append(readOnly, writable...)) {
		args = append(args, "--dir", path)
	}
	for _, path := range readOnly {
		args = append(args, "--ro-bind", path, path)
	}
	for _, path := range writable {
		args = append(args, "--bind", path, path)
	}
	args = append(args,
		"--bind", workspace, workspace,
		"--chdir", workspace,
		"--clearenv",
		"--setenv", "HOME", home,
		"--setenv", "USER", current.Username,
		"--setenv", "PATH", os.Getenv("PATH"),
		"--setenv", "LANG", envOr("LANG", "C.UTF-8"),
		"--setenv", "TERM", envOr("TERM", "xterm-256color"),
		"/bin/sh", "-lc", command,
	)
	return args, nil
}

func existingPaths(home string, relatives []string) []string {
	result := make([]string, 0, len(relatives))
	for _, relative := range relatives {
		path := filepath.Join(home, relative)
		if _, err := os.Stat(path); err == nil {
			result = append(result, path)
		}
	}
	return result
}

func appendParents(home string, paths []string) []string {
	seen := map[string]bool{}
	for _, path := range paths {
		parent := filepath.Dir(path)
		for within(home, parent) && parent != home {
			seen[parent] = true
			parent = filepath.Dir(parent)
		}
	}
	result := make([]string, 0, len(seen))
	for path := range seen {
		result = append(result, path)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.Count(result[i], string(filepath.Separator)) < strings.Count(result[j], string(filepath.Separator))
	})
	return result
}

func mountParents(root, path string) []string {
	current := filepath.Dir(path)
	result := []string{}
	for within(root, current) && current != root {
		result = append(result, current)
		current = filepath.Dir(current)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[output truncated]"
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
