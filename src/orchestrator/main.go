package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	"eve-beemo/src/orchestrator/llm"
	"eve-beemo/src/orchestrator/prompts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type serviceTarget struct {
	name string
	addr string
}

func dialService(name, addr string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServer
	cfg       config.Config
	toolsConn *grpc.ClientConn
	historyMu sync.Mutex
}

func (s *orchestratorServer) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	start := time.Now()
	userQuery := ""
	for i := len(req.GetMessages()) - 1; i >= 0; i-- {
		if req.GetMessages()[i].GetRole() == "user" {
			userQuery = req.GetMessages()[i].GetContent()
			break
		}
	}
	fmt.Printf("orch.chat start session=%s user_query=%q\n", req.GetSessionId(), userQuery)
	if userQuery == "" {
		fmt.Printf("orch.chat done session=%s status=empty_query ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "empty_query",
		})
		return &pb.ChatResponse{Text: ""}, nil
	}

	if s.cfg.LLMHTTPURL == "" {
		fmt.Printf("orch.chat done session=%s status=error reason=missing_llm_url ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     "missing_llm_url",
		})
		return nil, fmt.Errorf("LLM_HTTP_URL missing")
	}

	prompt := prompts.ToolDecision(userQuery)
	grammar, gerr := readGrammarFile(s.cfg.DecisionGrammarPath)
	if gerr != nil {
		fmt.Printf("orch.chat done session=%s status=error reason=grammar_read err=%v\n", req.GetSessionId(), gerr)
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     fmt.Sprintf("grammar_read: %v", gerr),
		})
		return nil, gerr
	}
	text, err := llm.CallCompletionWithGrammar(s.cfg.LLMCompletionsURL, s.cfg.LLMModel, prompt, grammar, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond)
	if err != nil {
		fmt.Printf("orch.chat done session=%s status=error reason=llm_call ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), err)
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     fmt.Sprintf("llm_decision: %v", err),
		})
		return nil, err
	}

	toolsRequested := parseToolList(text)
	fmt.Printf("orch.chat decision_raw session=%s text=%s tools=%v\n", req.GetSessionId(), text, toolsRequested)
	toolResult := ""
	if len(toolsRequested) > 0 {
		if s.toolsConn == nil {
			fmt.Printf("orch.chat done session=%s status=ok path=tool_missing_conn ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  text,
				Tools:     toolsRequested,
				Status:    "error",
				Error:     "tools_missing_conn",
			})
		} else {
			toolsClient := pb.NewToolsClient(s.toolsConn)
			for _, tool := range toolsRequested {
				fmt.Printf("orch.chat tool_call session=%s tool=%s\n", req.GetSessionId(), tool)
				toolResp, err := toolsClient.Execute(ctx, &pb.ToolRequest{
					SessionId: req.GetSessionId(),
					Action:    tool,
					Value:     "",
				})
				if err != nil {
					fmt.Printf("orch.chat done session=%s status=error reason=tool_call ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), err)
					s.appendHistory(&historyEntry{
						Timestamp: time.Now().Format(time.RFC3339),
						SessionID: req.GetSessionId(),
						UserQuery: userQuery,
						Decision:  text,
						Tools:     toolsRequested,
						Status:    "error",
						Error:     fmt.Sprintf("tool_call: %v", err),
					})
					return nil, err
				}
				toolResult = toolResult + fmt.Sprintf("tool=%s result=%s\n", tool, toolResp.GetResult())
			}
		}
	}

	followup := prompts.FinalResponse(userQuery, text, toolResult)
	finalText, err := llm.CallOnce(s.cfg.LLMHTTPURL, s.cfg.LLMModel, followup, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond)
	if err != nil {
		fmt.Printf("orch.chat done session=%s status=error reason=llm_followup ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), err)
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Decision:  text,
			Tools:     toolsRequested,
			ToolResult: strings.TrimSpace(toolResult),
			Status:    "error",
			Error:     fmt.Sprintf("llm_followup: %v", err),
		})
		return nil, err
	}
	s.appendHistory(&historyEntry{
		Timestamp:  time.Now().Format(time.RFC3339),
		SessionID:  req.GetSessionId(),
		UserQuery:  userQuery,
		Decision:   text,
		Tools:      toolsRequested,
		ToolResult: strings.TrimSpace(toolResult),
		Response:   finalText,
		Status:     "ok",
	})
	fmt.Printf("orch.chat done session=%s status=ok path=final ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
	return &pb.ChatResponse{Text: finalText}, nil
}

func parseToolList(text string) []string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return nil
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	var tools []string
	for _, p := range parts {
		t := strings.TrimSpace(strings.Trim(p, "\"'"))
		if t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

func readGrammarFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type historyEntry struct {
	Timestamp  string   `json:"timestamp"`
	SessionID  string   `json:"session_id"`
	UserQuery  string   `json:"user_query"`
	Decision   string   `json:"decision"`
	Tools      []string `json:"tools"`
	ToolResult string   `json:"tool_result"`
	Response   string   `json:"response"`
	Status     string   `json:"status"`
	Error      string   `json:"error,omitempty"`
}

func (s *orchestratorServer) appendHistory(entry *historyEntry) {
	if s.cfg.HistoryDir == "" {
		return
	}
	month := time.Now().Format("2006-01")
	path := fmt.Sprintf("%s/history-%s.jsonl", s.cfg.HistoryDir, month)

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
	data = append(data, '\n')

	s.historyMu.Lock()
	defer s.historyMu.Unlock()

	if err := os.MkdirAll(s.cfg.HistoryDir, 0755); err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
}

func main() {
	fmt.Println("eve-orchestrator: starting")

	cfg := config.Load()

	services := []serviceTarget{
		{name: "tools", addr: cfg.ToolsAddr},
		// {name: "vision", addr: cfg.VisionAddr},
		// {name: "ui", addr: cfg.UIAddr},
		// {name: "tts", addr: cfg.TTSAddr},
		// {name: "asr", addr: cfg.ASRAddr},
		// {name: "wakeword", addr: cfg.WakeWordAddr},
	}

	var conns []*grpc.ClientConn
	var toolsConn *grpc.ClientConn
	for _, svc := range services {
		if svc.addr == "" {
			fmt.Printf("svc=%s status=skip reason=env_missing\n", svc.name)
			continue
		}
		conn, err := dialService(svc.name, svc.addr)
		if err != nil {
			fmt.Printf("svc=%s status=error err=%v\n", svc.name, err)
			continue
		}
		fmt.Printf("svc=%s status=connected addr=%s\n", svc.name, svc.addr)
		conns = append(conns, conn)
		if svc.name == "tools" {
			toolsConn = conn
		}
	}

	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	orchAddr := ":5013"
	if cfg.OrchAddr != "" {
		orchAddr = cfg.OrchAddr
	}
	lis, err := net.Listen("tcp", orchAddr)
	if err != nil {
		fmt.Printf("orchestrator status=error listen_addr=%s err=%v\n", orchAddr, err)
		return
	}

	grpcServer := grpc.NewServer()
	pb.RegisterOrchestratorServer(grpcServer, &orchestratorServer{
		cfg:       cfg,
		toolsConn: toolsConn,
	})
	fmt.Printf("orchestrator status=listening addr=%s\n", orchAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Printf("orchestrator status=error err=%v\n", err)
	}

	for {
		time.Sleep(5 * time.Second)
	}
}
