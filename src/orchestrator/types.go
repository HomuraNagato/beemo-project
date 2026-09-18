package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	orchdb "eve-beemo/src/orchestrator/db"
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
	callAgentCompletion func(context.Context, string, string, string, int, time.Duration) (string, error)
	logger              *slog.Logger
	codeTools           pb.CodeToolsClient
	agentStore          agentEventStore
	approvalMu          sync.Mutex
	pendingApprovals    map[string]pendingApproval
	historyMu           sync.Mutex
	pendingMu           sync.Mutex
	pendingBySession    map[string]pendingToolState
	transcriptMu        sync.Mutex
	transcriptBySession map[string][]*pb.ChatMessage
	stateMu             sync.Mutex
	stateSubscribers    map[uint64]stateSubscriber
	stateSubscriberID   uint64
	coreContextMu       sync.Mutex
	coreContextCache    map[string]cachedCoreContext
}

type cachedCoreContext struct {
	body      string
	fetchedAt time.Time
}

type stateSubscriber struct {
	sessionID string
	updates   chan *pb.StateUpdate
}

type agentEventStore interface {
	UpsertSession(context.Context, string, string, string, string, string) error
	AppendEvent(context.Context, string, string, string, string, any) error
	UpdateSessionStatus(context.Context, string, string) error
	CreateApproval(context.Context, string, string, string, string) error
	DecideApproval(context.Context, string, bool) error
	ListSessions(context.Context, string, int) ([]orchdb.AgentSession, error)
	GetSession(context.Context, string, string) (orchdb.AgentSession, []orchdb.AgentEvent, error)
	CreateSession(context.Context, string, string) (orchdb.AgentSession, error)
	RenameSession(context.Context, string, string, string) (orchdb.AgentSession, error)
	DeleteSessions(context.Context, string, []string) (int64, error)
	SetSessionMode(context.Context, string, string, string, string) (orchdb.AgentSession, error)
	SetSessionInternet(context.Context, string, string, bool) (orchdb.AgentSession, error)
	ListProjects(context.Context, string) ([]orchdb.AgentProject, error)
	AddProject(context.Context, string, string, string, bool) (orchdb.AgentProject, error)
	RemoveProject(context.Context, string, string) (bool, error)
	UseProject(context.Context, string, string) (orchdb.AgentProject, error)
}

type pendingApproval struct {
	sessionID string
	decision  chan bool
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
	Tools      []string
	Path       string
	Status     string
	ErrorKind  string
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

func (s *orchestratorServer) sessionInternetEnabled(ctx context.Context, sessionID string) bool {
	if s.agentStore == nil || sessionID == "" {
		return false
	}
	session, _, err := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, sessionID)
	if err != nil {
		s.log().Warn("orch.internet.session_state", "session", sessionID, "err", err)
		return false
	}
	return session.InternetEnabled
}
