package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/chatctx"
	"eve-beemo/src/orchestrator/llm"
	"eve-beemo/src/orchestrator/prompts"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
)

func (s *orchestratorServer) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	start := time.Now()
	outcome, err := s.runChat(ctx, req)
	if err != nil {
		s.log().Error("orch.chat", "session", req.GetSessionId(), "status", "error", "err", err, "ms", time.Since(start).Milliseconds())
		return nil, err
	}
	if outcome.History.Status != "" {
		s.appendHistory(&outcome.History)
	}
	if outcome.Transcript != nil {
		s.setTranscript(req.GetSessionId(), outcome.Transcript)
	}
	s.log().Info("orch.chat", "session", req.GetSessionId(), "status", outcome.History.Status, "path", outcome.Path, "ms", time.Since(start).Milliseconds())
	return &pb.ChatResponse{Text: outcome.Response}, nil
}

func (s *orchestratorServer) runChat(ctx context.Context, req *pb.ChatRequest) (chatOutcome, error) {
	effectiveMessages := s.resolveMessages(req.GetSessionId(), req.GetMessages())
	userQuery := latestUserQuery(req.GetMessages())
	if userQuery == "" {
		userQuery = latestUserQuery(effectiveMessages)
	}
	s.log().Info("orch.chat.start", "session", req.GetSessionId(), "user_query", userQuery)
	if userQuery == "" {
		return chatOutcome{History: historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			Status:    "empty_query",
		}}, nil
	}

	if s.cfg.LLMHTTPURL == "" {
		return chatOutcome{History: historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     "missing_llm_url",
		}}, fmt.Errorf("LLM_HTTP_URL missing")
	}

	readGrammar := s.readGrammar
	if readGrammar == nil {
		readGrammar = readGrammarFile
	}
	grammar, err := readGrammar(s.cfg.DecisionGrammarPath)
	if err != nil {
		return chatOutcome{History: historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     fmt.Sprintf("grammar_read: %v", err),
		}}, err
	}

	callCompletion := s.callCompletion
	if callCompletion == nil {
		callCompletion = func(_ string, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return llm.CallDecisionWithGrammar(s.cfg.LLMProvider, s.cfg.LLMDecisionHTTPURL, model, prompt, grammar, timeout)
		}
	}
	callFinalMessage := s.callFinalMessage
	if callFinalMessage == nil {
		callFinalMessage = llm.CallOnce
	}

	callTimeout := time.Duration(s.cfg.LLMTimeoutMs) * time.Millisecond
	embeddingTimeout := time.Duration(s.cfg.EmbeddingTimeoutMs) * time.Millisecond
	activeContext := chatctx.Build(effectiveMessages, contextSelectionMessages, activeContextTurns)
	routingQuery := strings.TrimSpace(activeContext.UserEvidence)
	if routingQuery == "" {
		routingQuery = userQuery
	}

	decisionText := ""
	fromPending := false
	pending, hasPending := s.getPending(req.GetSessionId())
	originQuery := userQuery
	if hasPending && strings.TrimSpace(pending.OriginalUserQuery) != "" {
		originQuery = pending.OriginalUserQuery
	}

	if hasPending {
		pendingDecision, handled, cleared, perr := s.resumePendingDecision(pendingResumeRequest{
			sessionID:        req.GetSessionId(),
			pending:          pending,
			userQuery:        userQuery,
			activeTranscript: activeContext.Transcript,
			grammar:          grammar,
			callTimeout:      callTimeout,
			callCompletion:   callCompletion,
		})
		if perr != nil {
			return chatOutcome{}, perr
		}
		if handled {
			decisionText = pendingDecision.Text
			fromPending = pendingDecision.FromPending
		}
		if cleared {
			hasPending = false
			originQuery = userQuery
		}
	}

	var routeCandidates []routing.Candidate
	var catalogRoutes []routing.Route
	if selector, ok := s.routeSelector.(routeCatalog); ok {
		catalogRoutes = selector.Routes()
	}

	if strings.TrimSpace(decisionText) == "" {
		if inferred, ok, ierr := orchtools.InferToolCall(userQuery); ierr != nil {
			return chatOutcome{}, ierr
		} else if ok && supportedTool(inferred.Action) {
			inferredText, jerr := json.Marshal([]toolCall{fromPlannedCall(inferred)})
			if jerr != nil {
				return chatOutcome{}, jerr
			}
			decisionText = string(inferredText)
		}
	}

	if strings.TrimSpace(decisionText) == "" {
		prompt := prompts.ToolDecision(userQuery, activeContext.Transcript)
		if s.routeSelector != nil {
			candidates, rerr := s.routeSelector.Retrieve(routingQuery, embeddingTimeout)
			if rerr != nil {
				s.log().Warn("orch.chat.route_retrieval", "session", req.GetSessionId(), "status", "fallback", "err", rerr)
			} else if len(candidates) > 0 {
				routeCandidates = candidates
				prompt = prompts.RoutedToolDecision(userQuery, activeContext.Transcript, routing.FormatCandidates(candidates))
			}
		}
		decisionText, err = callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, prompt, grammar, callTimeout)
		if err != nil {
			return chatOutcome{History: historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Status:    "error",
				Error:     fmt.Sprintf("llm_decision: %v", err),
			}}, err
		}
	}

	toolCalls, err := parseToolCalls(decisionText)
	if err != nil {
		return chatOutcome{History: historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Decision:  decisionText,
			Status:    "error",
			Error:     fmt.Sprintf("tool_parse: %v", err),
		}}, err
	}

	if len(toolCalls) == 0 {
		retryPrompt := prompts.RetryToolDecision(userQuery, activeContext.Transcript)
		if retryText, rerr := callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, retryPrompt, grammar, callTimeout); rerr == nil {
			if retryCalls, perr := parseToolCalls(retryText); perr == nil && len(retryCalls) > 0 {
				toolCalls = retryCalls
				decisionText = retryText
			}
		}
	}

	toolCalls = filterSupportedToolCalls(toolCalls)
	if len(toolCalls) == 0 {
		decisionText = "[]"
	}
	toolsRequested := toolNames(toolCalls)
	s.log().Info("orch.chat.decision", "session", req.GetSessionId(), "text", decisionText, "tools", toolsRequested)

	toolResult := ""
	successfulResults := []orchtools.Result{}
	evidenceText := activeContext.UserEvidence
	if strings.TrimSpace(evidenceText) == "" {
		evidenceText = originQuery
		if hasPending && strings.TrimSpace(userQuery) != "" && userQuery != originQuery {
			evidenceText = originQuery + "\n" + userQuery
		}
	}

	for _, tool := range toolCalls {
		explicitTool := tool
		if !fromPending {
			groundedTool, gerr := orchtools.GroundCall(evidenceText, toPlannedCall(tool))
			if gerr != nil {
				return chatOutcome{}, gerr
			}
			explicitTool = fromPlannedCall(groundedTool)
		}
		if err := validateToolCall(routeCandidates, catalogRoutes, explicitTool); err != nil {
			return chatOutcome{}, err
		}

		resolvedTool := toPlannedCall(explicitTool)
		switch explicitTool.Tool {
		case "calculator":
			resolved, rerr := orchtools.ResolveCalculatorCall(resolvedTool, userQuery)
			if rerr != nil {
				return chatOutcome{}, rerr
			}
			resolvedTool = resolved
		case "weather":
			resolved, rerr := orchtools.ResolveWeatherCall(resolvedTool, userQuery)
			if rerr != nil {
				return chatOutcome{}, rerr
			}
			resolvedTool = resolved
			resolved, rerr = resolveWeatherLocation(ctx, s.tools, s.cfg, resolvedTool)
			if rerr != nil {
				return chatOutcome{}, rerr
			}
			resolvedTool = resolved
		case "older_sister":
			resolved, rerr := orchtools.ResolveOlderSisterCall(resolvedTool, userQuery)
			if rerr != nil {
				return chatOutcome{}, rerr
			}
			resolvedTool = resolved
		}

		tool = fromPlannedCall(resolvedTool)
		s.log().Info("orch.chat.tool_call", "session", req.GetSessionId(), "tool", tool.Tool, "args", string(tool.Args))
		result, rerr := s.tools.Execute(ctx, orchtools.Request{
			SessionID: req.GetSessionId(),
			Action:    tool.Tool,
			Args:      tool.Args,
		})
		if rerr != nil {
			return chatOutcome{History: historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  decisionText,
				Tools:     toolsRequested,
				Status:    "error",
				Error:     fmt.Sprintf("tool_call: %v", rerr),
			}}, rerr
		}
		if result.Status == "needs_input" {
			s.setPending(req.GetSessionId(), pendingToolState{
				OriginalUserQuery: originQuery,
				Tool:              tool.Tool,
				Args:              cloneRawMessage(tool.Args),
				Missing:           append([]string(nil), result.Missing...),
				Question:          result.Question,
			})
			return chatOutcome{
				Response:   result.Question,
				Path:       "needs_input",
				Transcript: appendAssistantMessage(effectiveMessages, result.Question),
				History: historyEntry{
					Timestamp:  time.Now().Format(time.RFC3339),
					SessionID:  req.GetSessionId(),
					UserQuery:  userQuery,
					Decision:   decisionText,
					Tools:      toolsRequested,
					ToolResult: fmt.Sprintf("status=%s missing=%v question=%s", result.Status, result.Missing, result.Question),
					Response:   result.Question,
					Status:     "needs_input",
				},
			}, nil
		}
		s.clearPending(req.GetSessionId())
		successfulResults = append(successfulResults, result)
		toolResult += fmt.Sprintf("tool=%s result=%s\n", result.Action, result.Output)
	}

	if directText, ok := directToolResponse(successfulResults, userQuery); ok {
		return chatOutcome{
			Response:   directText,
			Path:       "direct",
			Transcript: appendAssistantMessage(effectiveMessages, directText),
			History: historyEntry{
				Timestamp:  time.Now().Format(time.RFC3339),
				SessionID:  req.GetSessionId(),
				UserQuery:  userQuery,
				Decision:   decisionText,
				Tools:      toolsRequested,
				ToolResult: strings.TrimSpace(toolResult),
				Response:   directText,
				Status:     "ok",
			},
		}, nil
	}

	followup := prompts.FinalResponse(originQuery, userQuery, activeContext.Transcript, decisionText, toolResult)
	finalText, err := callFinalMessage(s.cfg.LLMHTTPURL, s.cfg.LLMModel, followup, callTimeout)
	if err != nil {
		return chatOutcome{History: historyEntry{
			Timestamp:  time.Now().Format(time.RFC3339),
			SessionID:  req.GetSessionId(),
			UserQuery:  userQuery,
			Decision:   decisionText,
			Tools:      toolsRequested,
			ToolResult: strings.TrimSpace(toolResult),
			Status:     "error",
			Error:      fmt.Sprintf("llm_followup: %v", err),
		}}, err
	}
	return chatOutcome{
		Response:   finalText,
		Path:       "final",
		Transcript: appendAssistantMessage(effectiveMessages, finalText),
		History: historyEntry{
			Timestamp:  time.Now().Format(time.RFC3339),
			SessionID:  req.GetSessionId(),
			UserQuery:  userQuery,
			Decision:   decisionText,
			Tools:      toolsRequested,
			ToolResult: strings.TrimSpace(toolResult),
			Response:   finalText,
			Status:     "ok",
		},
	}, nil
}
