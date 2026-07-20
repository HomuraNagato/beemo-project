package lifecycle

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	dir  string
	name string
	args []string
}

type recordingRunner struct {
	commands []recordedCommand
}

func (r *recordingRunner) Run(_ context.Context, dir, name string, args ...string) error {
	r.commands = append(r.commands, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
	return nil
}

func (r *recordingRunner) Output(_ context.Context, dir, name string, args ...string) (string, error) {
	r.commands = append(r.commands, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
	return `{"status":"SERVING"}`, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestUpUsesExistingMemoryPalaceComposeAfterModelServices(t *testing.T) {
	runner := &recordingRunner{}
	manager := Manager{
		Paths:   Paths{BeemoRoot: "/workspace/beemo-project", MemoryPalaceRoot: "/workspace/memory_palace"},
		Profile: Profile{Name: "garnetmoon", ComposeFiles: []string{"docker-compose.yaml", "docker-compose.gpu.yaml", "docker-compose.reranker.garnetmoon.yaml", "docker-compose.reranker.bge.yaml", "docker-compose.reranker.bge-gpu.yaml"}},
		Runner:  runner,
		Output:  io.Discard,
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
		})},
	}

	err := manager.Up(context.Background(), UpOptions{Memory: true, UI: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) < 5 {
		t.Fatalf("expected lifecycle commands, got %#v", runner.commands)
	}
	models := runner.commands[0]
	if models.dir != manager.Paths.BeemoRoot || !strings.Contains(strings.Join(models.args, " "), "eve-reranker") {
		t.Fatalf("expected Beemo model services first, got %#v", models)
	}
	memory := runner.commands[1]
	if memory.dir != manager.Paths.MemoryPalaceRoot || strings.Join(memory.args[:3], " ") != "compose -f docker-compose.yaml" {
		t.Fatalf("expected reused Memory Palace compose second, got %#v", memory)
	}
}

func TestRestartRoutesMemoryToItsOwnComposeProject(t *testing.T) {
	runner := &recordingRunner{}
	manager := Manager{Paths: Paths{BeemoRoot: "/beemo", MemoryPalaceRoot: "/memory"}, Profile: Profile{Name: "garnetmoon"}, Runner: runner}
	if err := manager.Restart(context.Background(), "memory_palace", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].dir != "/memory" {
		t.Fatalf("expected Memory Palace compose command, got %#v", runner.commands)
	}
}
