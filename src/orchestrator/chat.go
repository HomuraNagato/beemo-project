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
		outcome = normalizeErrorOutcome(req, outcome, err)
		s.log().Error("orch.chat", "session", req.GetSessionId(), "status", outcomeStatus(outcome), "error_kind", outcomeErrorKind(outcome), "err", err, "ms", time.Since(start).Milliseconds())
		if outcome.History.Status != "" {
			s.appendHistory(&outcome.History)
		}
		return &pb.ChatResponse{
			Text:      outcome.Response,
			Tools:     outcome.Tools,
			Status:    outcomeStatus(outcome),
			ErrorKind: outcomeErrorKind(outcome),
		}, nil
	}
	if outcome.History.Status != "" {
		s.appendHistory(&outcome.History)
	}
	if outcome.Transcript != nil {
		s.setTranscript(req.GetSessionId(), outcome.Transcript)
	}
	s.log().Info("orch.chat", "session", req.GetSessionId(), "status", outcomeStatus(outcome), "error_kind", outcomeErrorKind(outcome), "path", outcome.Path, "ms", time.Since(start).Milliseconds())
	return &pb.ChatResponse{
		Text:      outcome.Response,
		Tools:     outcome.Tools,
		Status:    outcomeStatus(outcome),
		ErrorKind: outcomeErrorKind(outcome),
	}, nil
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
	routingQuery := strings.TrimSpace(userQuery)

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
		if handled && strings.TrimSpace(pendingDecision.Response) != "" {
			response := strings.TrimSpace(pendingDecision.Response)
			return chatOutcome{
				Response:   response,
				Path:       "pending_cancel",
				Transcript: appendAssistantMessage(effectiveMessages, response),
				History: historyEntry{
					Timestamp: time.Now().Format(time.RFC3339),
					SessionID: req.GetSessionId(),
					UserQuery: userQuery,
					Decision:  "pending_cancel",
					Response:  response,
					Status:    "ok",
				},
			}, nil
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

	if strings.TrimSpace(decisionText) == "" && s.cfg.DeterministicToolShortcuts {
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
		decisionGrammar := grammar
		if s.routeSelector != nil {
			candidates, rerr := s.routeSelector.Retrieve(routingQuery, embeddingTimeout)
			if rerr != nil {
				s.log().Warn("orch.chat.route_retrieval", "session", req.GetSessionId(), "status", "fallback", "err", rerr)
			} else if len(candidates) > 0 {
				routeCandidates = candidates
				s.log().Info("orch.chat.route_candidates", "session", req.GetSessionId(), "candidates", routeCandidateLabels(candidates))
				routePrompt := prompts.RouteDecision(userQuery, activeContext.Transcript, routing.FormatCandidates(candidates))
				routeText, derr := callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, routePrompt, routeDecisionGrammar, callTimeout)
				if derr != nil {
					return chatOutcome{History: historyEntry{
						Timestamp: time.Now().Format(time.RFC3339),
						SessionID: req.GetSessionId(),
						UserQuery: userQuery,
						Status:    "error",
						Error:     fmt.Sprintf("route_decision: %v", derr),
					}}, derr
				}
				selectedCandidate, perr := parseRouteDecision(routeText, candidates)
				if perr != nil {
					return chatOutcome{History: historyEntry{
						Timestamp: time.Now().Format(time.RFC3339),
						SessionID: req.GetSessionId(),
						UserQuery: userQuery,
						Decision:  routeText,
						Status:    "error",
						Error:     fmt.Sprintf("route_parse: %v", perr),
					}}, perr
				}
				if verr := validateSelectedRoute(selectedCandidate, userQuery); verr != nil {
					return chatOutcome{History: historyEntry{
						Timestamp: time.Now().Format(time.RFC3339),
						SessionID: req.GetSessionId(),
						UserQuery: userQuery,
						Decision:  routeText,
						Status:    "error",
						Error:     fmt.Sprintf("route_validation: %v", verr),
					}}, verr
				}
				routeCandidates = []routing.Candidate{selectedCandidate}
				s.log().Info("orch.chat.route_decision", "session", req.GetSessionId(), "decision", routeText, "route", strings.TrimSpace(selectedCandidate.Route.ID))
				if routedCall, ok, cerr := toolCallFromRoute(selectedCandidate); cerr != nil {
					return chatOutcome{}, cerr
				} else if ok {
					raw, merr := json.Marshal([]toolCall{routedCall})
					if merr != nil {
						return chatOutcome{}, merr
					}
					decisionText = string(raw)
				} else {
					prompt = prompts.RouteToolExtraction(userQuery, activeContext.Transcript, selectedRouteBlock(selectedCandidate))
					decisionGrammar = requireRouteToolCallGrammar(grammar, selectedCandidate.Route)
				}
			}
		}
		if strings.TrimSpace(decisionText) == "" {
			decisionText, err = callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, prompt, decisionGrammar, callTimeout)
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
	}

	toolCalls, err := parseToolCalls(decisionText)
	if err != nil {
		retryPrompt := prompts.RetryToolDecision(userQuery, activeContext.Transcript)
		retryGrammar := grammar
		if len(routeCandidates) > 0 {
			retryPrompt = prompts.RouteToolExtraction(userQuery, activeContext.Transcript, selectedRouteBlock(routeCandidates[0]))
			retryGrammar = requireRouteToolCallGrammar(grammar, routeCandidates[0].Route)
		}
		if retryText, rerr := callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, retryPrompt, retryGrammar, callTimeout); rerr == nil {
			if retryCalls, perr := parseToolCalls(retryText); perr == nil {
				toolCalls = retryCalls
				decisionText = retryText
				err = nil
			}
		}
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
	}

	if len(toolCalls) == 0 {
		retryPrompt := prompts.RetryToolDecision(userQuery, activeContext.Transcript)
		retryGrammar := grammar
		if len(routeCandidates) > 0 {
			retryPrompt = prompts.RouteToolExtraction(userQuery, activeContext.Transcript, selectedRouteBlock(routeCandidates[0]))
			retryGrammar = requireRouteToolCallGrammar(grammar, routeCandidates[0].Route)
		}
		if retryText, rerr := callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, retryPrompt, retryGrammar, callTimeout); rerr == nil {
			if retryCalls, perr := parseToolCalls(retryText); perr == nil && len(retryCalls) > 0 {
				toolCalls = retryCalls
				decisionText = retryText
			}
		}
		if len(routeCandidates) > 0 && len(toolCalls) == 0 {
			err := fmt.Errorf("routed decision returned no tool calls")
			return chatOutcome{History: historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  decisionText,
				Status:    "error",
				Error:     fmt.Sprintf("tool_parse: %v", err),
			}}, err
		}
	}

	toolCalls = filterSupportedToolCalls(toolCalls)
	if len(routeCandidates) > 0 && len(toolCalls) == 0 {
		err := fmt.Errorf("routed decision returned no supported tool calls")
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
		if strings.TrimSpace(tool.Tool) == "beemo.direct" {
			s.log().Info("orch.chat.direct_choice", "session", req.GetSessionId(), "tool", tool.Tool)
			continue
		}

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
			if validationResult, handled, verr := orchtools.ValidateCalculatorCall(resolvedTool, userQuery); verr != nil {
				return chatOutcome{}, verr
			} else if handled {
				s.setPending(req.GetSessionId(), pendingToolState{
					OriginalUserQuery: originQuery,
					Tool:              explicitTool.Tool,
					Args:              cloneRawMessage(resolvedTool.Args),
					Missing:           append([]string(nil), validationResult.Missing...),
					Question:          validationResult.Question,
				})
				return chatOutcome{
					Response:   validationResult.Question,
					Tools:      toolsRequested,
					Path:       "needs_input",
					Transcript: appendAssistantMessage(effectiveMessages, validationResult.Question),
					History: historyEntry{
						Timestamp:  time.Now().Format(time.RFC3339),
						SessionID:  req.GetSessionId(),
						UserQuery:  userQuery,
						Decision:   decisionText,
						Tools:      toolsRequested,
						ToolResult: fmt.Sprintf("status=%s missing=%v question=%s", validationResult.Status, validationResult.Missing, validationResult.Question),
						Response:   validationResult.Question,
						Status:     "needs_input",
					},
					Status:    "needs_input",
					ErrorKind: "missing_input",
				}, nil
			}
		case "weather":
			resolved, rerr := orchtools.ResolveWeatherCall(resolvedTool, userQuery)
			if rerr != nil {
				return chatOutcome{}, rerr
			}
			resolvedTool = resolved
			resolved, rerr = resolveWeatherLocation(ctx, s.tools, s.cfg, resolvedTool)
			if rerr != nil {
				s.setPending(req.GetSessionId(), pendingToolState{
					OriginalUserQuery: originQuery,
					Tool:              explicitTool.Tool,
					Args:              cloneRawMessage(explicitTool.Args),
					Missing:           []string{"location"},
					Question:          "I could not find that location. What city and state should I use for the weather?",
				})
				response := "I could not find that location. What city and state should I use for the weather?"
				return chatOutcome{
					Response:   response,
					Tools:      toolsRequested,
					Path:       "needs_input",
					Transcript: appendAssistantMessage(effectiveMessages, response),
					History: historyEntry{
						Timestamp:  time.Now().Format(time.RFC3339),
						SessionID:  req.GetSessionId(),
						UserQuery:  userQuery,
						Decision:   decisionText,
						Tools:      toolsRequested,
						ToolResult: fmt.Sprintf("status=needs_input missing=[location] question=%s error=%v", response, rerr),
						Response:   response,
						Status:     "needs_input",
					},
					Status:    "needs_input",
					ErrorKind: "location_not_found",
				}, nil
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
				Tools:      toolsRequested,
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
				Status:    "needs_input",
				ErrorKind: "missing_input",
			}, nil
		}
		s.clearPending(req.GetSessionId())
		successfulResults = append(successfulResults, result)
		toolResult += fmt.Sprintf("tool=%s result=%s\n", result.Action, toolResultForPrompt(result))
	}

	if directText, ok := directToolResponse(s.cfg.DirectToolResponses, successfulResults, userQuery); ok {
		return chatOutcome{
			Response:   directText,
			Tools:      toolsRequested,
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
		Tools:      toolsRequested,
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

func routeCandidateLabels(candidates []routing.Candidate) []string {
	labels := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		routeID := strings.TrimSpace(candidate.Route.ID)
		if routeID == "" {
			routeID = strings.TrimSpace(candidate.Route.Handler.Target)
		}
		if routeID == "" {
			routeID = "unknown"
		}
		labels = append(labels, fmt.Sprintf("%s:%.3f", routeID, candidate.Score))
	}
	return labels
}

func requireSingleToolCallGrammar(grammar string) string {
	return strings.Replace(grammar, "root ::= empty_list | single_call_list", "root ::= single_call_list", 1)
}

func normalizeErrorOutcome(req *pb.ChatRequest, outcome chatOutcome, err error) chatOutcome {
	status := outcomeStatus(outcome)
	if status == "" || status == "ok" {
		status = "error"
	}
	kind := outcomeErrorKind(outcome)
	if kind == "" {
		kind = classifyChatError(outcome.History.Error, err)
	}
	if outcome.Response == "" {
		outcome.Response = responseForErrorKind(kind)
	}
	if outcome.History.Timestamp == "" {
		outcome.History.Timestamp = time.Now().Format(time.RFC3339)
	}
	if outcome.History.SessionID == "" {
		outcome.History.SessionID = req.GetSessionId()
	}
	if outcome.History.UserQuery == "" {
		outcome.History.UserQuery = latestUserQuery(req.GetMessages())
	}
	outcome.History.Status = status
	if outcome.History.Error == "" && err != nil {
		outcome.History.Error = fmt.Sprintf("%s: %v", kind, err)
	}
	outcome.Status = status
	outcome.ErrorKind = kind
	return outcome
}

func outcomeStatus(outcome chatOutcome) string {
	if strings.TrimSpace(outcome.Status) != "" {
		return strings.TrimSpace(outcome.Status)
	}
	if strings.TrimSpace(outcome.History.Status) != "" {
		return strings.TrimSpace(outcome.History.Status)
	}
	return "ok"
}

func outcomeErrorKind(outcome chatOutcome) string {
	if strings.TrimSpace(outcome.ErrorKind) != "" {
		return strings.TrimSpace(outcome.ErrorKind)
	}
	return classifyChatError(outcome.History.Error, nil)
}

func classifyChatError(historyError string, err error) string {
	text := strings.TrimSpace(historyError)
	if text == "" && err != nil {
		text = err.Error()
	}
	switch {
	case strings.Contains(text, "tool_parse"):
		return "tool_parse"
	case strings.Contains(text, "route_parse"):
		return "route_parse"
	case strings.Contains(text, "llm_decision"):
		return "llm_decision"
	case strings.Contains(text, "llm_followup"):
		return "llm_followup"
	case strings.Contains(text, "tool_call"):
		return "tool_call"
	case strings.Contains(text, "grammar_read"):
		return "grammar_read"
	case strings.Contains(text, "missing_llm_url"):
		return "missing_llm_url"
	case strings.Contains(text, "route"):
		return "route_validation"
	case strings.Contains(text, "ground"):
		return "tool_grounding"
	}
	if text == "" {
		return ""
	}
	return "internal_error"
}

func responseForErrorKind(kind string) string {
	switch kind {
	case "tool_parse":
		return "I could not format the tool request cleanly. Please try that again."
	case "llm_decision":
		return "I had trouble choosing the right tool. Please try that again."
	case "llm_followup":
		return "I got the tool result, but had trouble wording the answer. Please try that again."
	case "tool_call":
		return "That tool hit an error. Please try again."
	case "grammar_read", "missing_llm_url":
		return "A local Beemo service is not configured correctly."
	case "route_parse":
		return "I could not format the route decision cleanly. Please try that again."
	case "route_validation", "tool_grounding":
		return "I could not line up that request with the selected tool. Please try that again."
	default:
		return "I hit an internal routing error. Please try that again."
	}
}
