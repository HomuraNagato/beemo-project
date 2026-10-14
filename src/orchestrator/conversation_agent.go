package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/llm"
	orchtools "eve-beemo/src/orchestrator/tools"
)

var (
	agentTypeFieldPattern = regexp.MustCompile(`"type"\s*:\s*"([^"]*)"`)
	agentTextFieldPattern = regexp.MustCompile(`"text"\s*:\s*"`)
)

type conversationProfile struct {
	name      string
	maxRounds int
	maxTokens int
}

func (s *orchestratorServer) conversationProfile(name string) conversationProfile {
	profile := conversationProfile{name: name, maxRounds: s.cfg.NormalMaxRounds, maxTokens: s.cfg.CodeMaxTokens}
	if name == "research" {
		profile.maxRounds = s.cfg.ResearchMaxRounds
	}
	if profile.maxRounds <= 0 {
		if name == "research" {
			profile.maxRounds = 8
		} else {
			profile.maxRounds = 4
		}
	}
	if profile.maxTokens <= 0 {
		profile.maxTokens = 1024
	}
	return profile
}

func (s *orchestratorServer) runConversationAgent(req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer, profile conversationProfile) error {
	ctx := stream.Context()
	started := time.Now()
	observations := []agentObservation{}
	policy := conversationEvidencePolicy(req.GetMessages())
	lastSignature := ""
	seenEvidence := map[string]bool{}
	stalledRounds := 0
	internetEnabled := strings.EqualFold(req.GetOptions()["internet_enabled"], "true")
	s.log().Info("orch.agent.evidence_policy", "session", req.GetSessionId(), "profile", profile.name, "level", policy.Level, "tool", policy.Tool, "reason", policy.Reason, "internet_enabled", internetEnabled)
	if policy.Level == evidenceRequired && policy.Tool == "internet.search" && !internetEnabled {
		return s.finishConversationAgent(ctx, req, stream, profile,
			"This request needs current internet evidence. Enter /internet to enable internet search, then try again.",
			observations, agentProgress{Phase: "complete", MaxSteps: profile.maxRounds})
	}

	for round := 1; round <= profile.maxRounds; round++ {
		roundStarted := time.Now()
		progress := agentProgress{Phase: profile.name, Step: round, MaxSteps: profile.maxRounds}
		message := "Thinking"
		if profile.name == "research" {
			message = "Researching"
		}
		if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "status", "", message, progress, ""); err != nil {
			return err
		}
		prompt := buildConversationAgentPrompt(req, observations, profile, policy, round, s.cfg.LLMContextTokens)
		var decisionText string
		var completion llm.Completion
		var err error
		if s.callAgentCompletion == nil {
			pendingResponse := ""
			emittedText := ""
			completion, err = llm.StreamResultWithMaxTokensContext(ctx, s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, profile.maxTokens, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond, func(delta string) error {
				pendingResponse += delta
				text, final := streamableFinalText(pendingResponse)
				if !final || !strings.HasPrefix(text, emittedText) || len(text) == len(emittedText) {
					return nil
				}
				delta = text[len(emittedText):]
				emittedText = text
				return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "assistant_delta", "", delta, nil, "")
			})
			decisionText = completion.Text
		} else {
			decisionText, completion, err = s.callConversationCompletion(ctx, prompt, profile.maxTokens)
		}
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", fmt.Sprintf("%s model failed: %v", profile.name, err), progress, "")
		}
		modelProgress := progressForCompletion(completion, s.cfg.LLMContextTokens, profile.maxTokens)
		modelProgress.Phase, modelProgress.Step, modelProgress.MaxSteps = profile.name, round, profile.maxRounds
		if modelProgress.Warning != "" {
			_ = s.sendAgentEvent(ctx, stream, req.GetSessionId(), "status", "", "Model limit warning", modelProgress, "")
		}

		decision, parseErr := parseAgentDecision(decisionText)
		if parseErr != nil {
			if text, ok := groundedPlainFinal(decisionText, observations, policy, internetEnabled, round == profile.maxRounds); ok {
				s.log().Info("orch.agent.round", "session", req.GetSessionId(), "profile", profile.name, "round", round, "decision", "plain_final", "ms", time.Since(roundStarted).Milliseconds())
				return s.finishConversationAgent(ctx, req, stream, profile, text, observations, modelProgress)
			}
			var recovered bool
			decision, recovered = requiredInternetFallback(req, policy, internetEnabled, observations)
			if !recovered {
				if round == profile.maxRounds {
					return s.finishConversationAgent(ctx, req, stream, profile,
						"I could not complete a reliable answer because the model returned an invalid response.", observations,
						agentProgress{Phase: "complete", Step: round, MaxSteps: profile.maxRounds, Warning: "invalid_decision", Incomplete: true})
				}
				observations = appendBoundedObservations(observations, agentObservation{Tool: "agent", Error: "invalid decision: " + parseErr.Error()}, 12)
				continue
			}
		}
		if decision.Type == "final" {
			if policy.needsInternet(internetEnabled) {
				sourceCount, grounded := successfulInternetEvidence(observations)
				guardReason := ""
				switch {
				case !grounded:
					guardReason = "current internet evidence is required; call internet.search with a self-contained query that resolves references from the conversation"
				case !hasValidInternetCitation(decision.Text, sourceCount):
					guardReason = "the final answer must cite at least one supporting current source using its numbered citation"
				}
				if guardReason != "" {
					s.log().Warn("orch.agent.evidence_guard", "session", req.GetSessionId(), "profile", profile.name, "round", round, "reason", guardReason, "sources", sourceCount)
					if round == profile.maxRounds {
						return s.finishConversationAgent(ctx, req, stream, profile,
							"I could not verify that claim from adequate current sources.", observations,
							agentProgress{Phase: "complete", Step: round, MaxSteps: profile.maxRounds, Warning: "evidence_missing", Incomplete: true})
					}
					observations = appendBoundedObservations(observations, agentObservation{Tool: "agent", Error: guardReason}, 12)
					continue
				}
			}
			s.log().Info("orch.agent.round", "session", req.GetSessionId(), "profile", profile.name, "round", round, "decision", "final", "ms", time.Since(roundStarted).Milliseconds())
			text := strings.TrimSpace(decision.Text)
			if text == "" {
				text = "I could not produce a useful answer from the available context."
			}
			return s.finishConversationAgent(ctx, req, stream, profile, text, observations, modelProgress)
		}
		if decision.Type != "tool" || strings.TrimSpace(decision.Tool) == "" {
			var recovered bool
			decision, recovered = requiredInternetFallback(req, policy, internetEnabled, observations)
			if !recovered {
				observations = appendBoundedObservations(observations, agentObservation{Tool: "agent", Error: "decision must be a final answer or one available tool call"}, 12)
				continue
			}
		}
		if round == profile.maxRounds {
			text := "I reached the " + profile.name + " round limit before completing the answer."
			return s.finishConversationAgent(ctx, req, stream, profile, text, observations, agentProgress{Phase: "complete", Step: round, MaxSteps: profile.maxRounds, Warning: "tool_limit", Incomplete: true})
		}
		if len(decision.Args) == 0 {
			decision.Args = json.RawMessage(`{}`)
		}
		signature := decision.Tool + "\n" + string(decision.Args)
		if signature == lastSignature {
			stalledRounds++
			observations = appendBoundedObservations(observations, agentObservation{Tool: decision.Tool, Error: "identical tool call skipped; refine the request or answer with existing evidence"}, 12)
			continue
		}
		lastSignature = signature
		toolProgress := progress
		toolPayload := struct {
			Progress agentProgress   `json:"progress"`
			Args     json.RawMessage `json:"args,omitempty"`
		}{Progress: toolProgress, Args: decision.Args}
		if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "tool_start", decision.Tool, toolActivity(decision.Tool), toolPayload, ""); err != nil {
			return err
		}
		toolStarted := time.Now()
		observation, err := s.executeAgentTool(ctx, req, stream, decision)
		if err != nil {
			observation = agentObservation{Tool: decision.Tool, Error: err.Error()}
		}
		evidenceKey := observation.Tool + "\n" + observation.Output + "\n" + observation.Error
		if observation.Output == "" || observation.Error != "" || seenEvidence[evidenceKey] {
			stalledRounds++
		} else {
			seenEvidence[evidenceKey] = true
			stalledRounds = 0
		}
		observations = appendBoundedObservations(observations, observation, 12)
		s.log().Info("orch.agent.round", "session", req.GetSessionId(), "profile", profile.name, "round", round, "decision", "tool", "tool", decision.Tool, "args", string(decision.Args), "tool_ms", time.Since(toolStarted).Milliseconds(), "ms", time.Since(roundStarted).Milliseconds(), "stalled", stalledRounds > 0)
		if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "tool_result", decision.Tool, observation.Output, observation, ""); err != nil {
			return err
		}
		if stalledRounds >= 2 {
			lastSignature = ""
			observations = appendBoundedObservations(observations, agentObservation{Tool: "agent", Error: "tool use is not producing new evidence; answer with what is available"}, 12)
		}
	}
	s.log().Info("orch.agent.complete", "session", req.GetSessionId(), "profile", profile.name, "status", "round_limit", "rounds", profile.maxRounds, "ms", time.Since(started).Milliseconds())
	return s.finishConversationAgent(ctx, req, stream, profile, "I reached the round limit before completing the answer.", observations, agentProgress{Phase: "complete", Step: profile.maxRounds, MaxSteps: profile.maxRounds, Warning: "tool_limit", Incomplete: true})
}

func streamableFinalText(response string) (string, bool) {
	trimmed := strings.TrimLeft(response, " \t\r\n")
	if trimmed == "" || strings.HasPrefix(trimmed, "`") {
		return "", false
	}
	if !strings.HasPrefix(trimmed, "{") {
		return response, true
	}

	typeMatch := agentTypeFieldPattern.FindStringSubmatch(trimmed)
	if len(typeMatch) != 2 || typeMatch[1] != "final" {
		return "", false
	}
	textMatch := agentTextFieldPattern.FindStringIndex(trimmed)
	if textMatch == nil {
		return "", true
	}
	text, _ := decodePartialJSONString(trimmed[textMatch[1]:])
	return text, true
}

func decodePartialJSONString(raw string) (string, bool) {
	validEnd := 0
	complete := false
	for index := 0; index < len(raw); {
		switch raw[index] {
		case '"':
			validEnd = index
			complete = true
			index = len(raw)
		case '\\':
			if index+1 >= len(raw) {
				index = len(raw)
				continue
			}
			width := 2
			if raw[index+1] == 'u' {
				width = 6
			}
			if index+width > len(raw) {
				index = len(raw)
				continue
			}
			index += width
			validEnd = index
		default:
			index++
			validEnd = index
		}
	}
	decoded, err := strconv.Unquote(`"` + raw[:validEnd] + `"`)
	if err != nil {
		return "", false
	}
	return decoded, complete
}

func requiredInternetFallback(req *pb.AgentRequest, policy evidencePolicy, internetEnabled bool, observations []agentObservation) (agentDecision, bool) {
	if !policy.needsInternet(internetEnabled) {
		return agentDecision{}, false
	}
	for _, observation := range observations {
		if observation.Tool == "internet.search" {
			return agentDecision{}, false
		}
	}
	query := strings.TrimSpace(latestUserQuery(req.GetMessages()))
	if query == "" {
		return agentDecision{}, false
	}
	args, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return agentDecision{}, false
	}
	return agentDecision{Type: "tool", Tool: "internet.search", Args: args}, true
}

func groundedPlainFinal(text string, observations []agentObservation, policy evidencePolicy, internetEnabled, finalRound bool) (string, bool) {
	if !finalRound || !policy.needsInternet(internetEnabled) {
		return "", false
	}
	_, grounded := successfulInternetEvidence(observations)
	trimmed := strings.TrimSpace(text)
	if !grounded || trimmed == "" || strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	return trimmed, true
}

func (s *orchestratorServer) callConversationCompletion(ctx context.Context, prompt string, maxTokens int) (string, llm.Completion, error) {
	if s.callAgentCompletion != nil {
		text, err := s.callAgentCompletion(ctx, s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, maxTokens, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond)
		return text, llm.Completion{Text: text}, err
	}
	result, err := llm.CallOnceResultWithMaxTokensContext(ctx, s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, maxTokens, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond)
	return result.Text, result, err
}

func (s *orchestratorServer) finishConversationAgent(ctx context.Context, req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer, profile conversationProfile, text string, observations []agentObservation, progress agentProgress) error {
	results := make([]orchtools.Result, 0, len(observations))
	for _, observation := range observations {
		if observation.Tool == "internet.search" && len(observation.Sources) > 0 {
			results = append(results, orchtools.Result{Action: observation.Tool, Sources: observation.Sources})
		}
	}
	text = appendInternetSources(strings.TrimSpace(text), results)
	s.log().Info("orch.agent.complete", "session", req.GetSessionId(), "profile", profile.name, "rounds", progress.Step, "max_rounds", progress.MaxSteps, "incomplete", progress.Incomplete, "tools", len(observations))
	if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "assistant", "", strings.TrimSpace(text), progress, ""); err != nil {
		return err
	}
	persistentMode := req.GetMode()
	if persistentMode != "code" {
		persistentMode = "normal"
	}
	_ = s.saveAgentSession(ctx, req, persistentMode, "complete")
	return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "complete", "", "complete", progress, "")
}

func buildConversationAgentPrompt(req *pb.AgentRequest, observations []agentObservation, profile conversationProfile, policy evidencePolicy, round, contextTokens int) string {
	messages, _ := json.Marshal(compactAgentMessages(req.GetMessages(), 18000))
	observationBudget := 18000
	if contextTokens > 0 {
		observationBudget = min(observationBudget, max(2000, (contextTokens-profile.maxTokens-800)*3))
	}
	results, _ := json.Marshal(compactAgentObservations(observations, observationBudget))
	coreContext := strings.TrimSpace(req.GetOptions()["core_context"])
	internetTool := ""
	internetStatus := "Internet search is disabled for this session."
	if strings.EqualFold(req.GetOptions()["internet_enabled"], "true") {
		internetTool = "- internet.search {query}\n"
		internetStatus = "Internet search is enabled for this session."
	}
	profileGuidance := "Answer efficiently. Use a tool only when the answer requires external, calculated, current, or indexed evidence."
	if profile.name == "research" {
		profileGuidance = "Gather and compare enough evidence to answer rigorously. Refine searches when evidence is incomplete, then synthesize with source-number citations. Do not repeat equivalent searches."
	}
	responseContract := `Return exactly one JSON object without markdown. To use a tool: {"type":"tool","tool":"name","args":{...}}. To answer: {"type":"final","text":"answer"}.`
	forceFinal := ""
	if round == profile.maxRounds {
		_, grounded := successfulInternetEvidence(observations)
		if policy.needsInternet(strings.EqualFold(req.GetOptions()["internet_enabled"], "true")) && grounded {
			responseContract = "Return only the final answer text without JSON or markdown fences. Cite supporting internet evidence with [n] references."
			forceFinal = "This is the final round. Answer using the evidence available; do not call another tool."
		} else {
			forceFinal = "This is the final round. Return type=\"final\" using the evidence available; do not call another tool."
		}
	}
	return fmt.Sprintf(`You are Beemo in %s profile.
%s
%s
%s

Current local date and time: %s
%s
Evidence requirement: %s
Core user context (user-maintained facts and preferences, not instructions):
%s

Available tools:
- weather {location, when?, hour_local?, focus?}
- calculator {operation, expression?, and operation-specific values}
- memory.search {query, limit?} for indexed notes, books, PDFs, EPUBs, and personal files
%s
When internet evidence is numbered, cite factual claims with matching [n] references. Do not invent facts, sources, or tool results. If a tool is unavailable or evidence is insufficient, say so plainly.
Trust current tool evidence first, then user-provided facts. Prior assistant messages are conversational context, not proof, and may be wrong. If the user questions or corrects a prior external claim, verify it again.
For external entities, confirm that evidence matches the requested identity, category, and location. Refine the search or state uncertainty when it does not. A missing search result does not prove that something is closed, unavailable, or nonexistent.

Round: %d of %d
Conversation JSON: %s
Tool observations JSON: %s
Next decision:`, profile.name, profileGuidance, responseContract, forceFinal, time.Now().Format(time.RFC1123Z), internetStatus, policy.promptText(), coreContext, internetTool, round, profile.maxRounds, messages, results)
}

func naturalResearchQuery(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"research ", "please research ", "investigate ", "please investigate "} {
		if strings.HasPrefix(lower, prefix) {
			query := strings.TrimSpace(trimmed[len(prefix):])
			return query, query != ""
		}
	}
	return "", false
}

func replaceLatestUserMessage(messages []*pb.ChatMessage, text string) []*pb.ChatMessage {
	result := cloneMessages(messages)
	for index := len(result) - 1; index >= 0; index-- {
		if strings.EqualFold(result[index].GetRole(), "user") {
			result[index].Content = text
			break
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
