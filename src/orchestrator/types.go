package main

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
)

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServer
	cfg                 config.Config
	tools               orchtools.Executor
	routeSelector       routeSelector
	readGrammar         func(path string) (string, error)
	callCompletion      func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error)
	callFinalMessage    func(httpURL, model, prompt string, timeout time.Duration) (string, error)
	logger              *slog.Logger
	historyMu           sync.Mutex
	pendingMu           sync.Mutex
	pendingBySession    map[string]pendingToolState
	transcriptMu        sync.Mutex
	transcriptBySession map[string][]*pb.ChatMessage
}

type toolCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type pendingToolState struct {
	OriginalUserQuery string          `json:"original_user_query"`
	Tool              string          `json:"tool"`
	Args              json.RawMessage `json:"args"`
	Missing           []string        `json:"missing"`
	Question          string          `json:"question"`
}

type routeSelector interface {
	Retrieve(query string, timeout time.Duration) ([]routing.Candidate, error)
}

type routeCatalog interface {
	Routes() []routing.Route
}

type weatherConfigProvider interface {
	WeatherConfig() orchtools.WeatherConfig
}

type chatOutcome struct {
	Response   string
	Path       string
	History    historyEntry
	Transcript []*pb.ChatMessage
}

const (
	contextSelectionMessages  = 16
	activeContextTurns        = 4
	sessionTranscriptMessages = 18
)

func (s *orchestratorServer) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}
