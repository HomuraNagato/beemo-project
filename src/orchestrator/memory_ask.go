package main

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/llm"
	orchtools "eve-beemo/src/orchestrator/tools"
)

const (
	memoryAskSourceLimit    = 6
	memoryAskCandidateLimit = 40
	memoryAskSourceChars    = 1000
)

func (s *orchestratorServer) runMemoryAsk(ctx context.Context, req *pb.ChatRequest, userQuery string, effectiveMessages []*pb.ChatMessage, callFinalMessage func(string, string, string, time.Duration) (string, error), callTimeout time.Duration) (chatOutcome, error) {
	start := time.Now()
	profile := memoryAnswerProfileFor(userQuery)
	requestID := memoryAskOption(req.GetOptions(), "request_id", fallbackRequestID(req.GetSessionId()))
	memoryCfg := orchtools.MemoryConfig{
		BaseURL:   s.cfg.MemoryBaseURL,
		UserKey:   memoryAskOption(req.GetOptions(), "memory_user_key", s.cfg.MemoryUserKey),
		TimeoutMs: s.cfg.MemoryTimeoutMs,
		AutoSave:  s.cfg.MemoryAutoSave,
	}
	retrieved, err := orchtools.RetrieveMemory(ctx, memoryCfg, orchtools.MemoryRetrieveRequest{
		RequestID:           requestID,
		Query:               userQuery,
		Mode:                memoryAskOption(req.GetOptions(), "memory_search_mode", "hybrid"),
		TimeFilter:          memoryAskOption(req.GetOptions(), "memory_time_filter", "all"),
		Scope:               memoryAskOption(req.GetOptions(), "memory_scope", ""),
		Collection:          memoryAskOption(req.GetOptions(), "memory_collection", ""),
		Limit:               profile.sourceLimit,
		CandidateLimit:      memoryAskCandidateLimit,
		ChunkLimitPerSource: memoryAskCandidateLimit,
	})
	retrieveMs := time.Since(start).Milliseconds()
	if err != nil {
		s.log().Error("orch.memory_ask.retrieve", "request_id", requestID, "session", req.GetSessionId(), "status", "error", "query", userQuery, "query_chars", len([]rune(userQuery)), "ms", retrieveMs, "err", err)
		return chatOutcome{History: historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Tools:     []string{"memory.retrieve"},
			Status:    "error",
			Error:     fmt.Sprintf("memory_retrieve: %v", err),
		}}, err
	}
	s.log().Info(
		"orch.memory_ask.retrieve",
		"request_id", requestID,
		"session", req.GetSessionId(),
		"status", "ok",
		"query", userQuery,
		"query_chars", len([]rune(userQuery)),
		"sources", len(retrieved.Sources),
		"candidates", retrieved.Diagnostics.CandidateCount,
		"search_ms", retrieved.Diagnostics.SearchMs,
		"rerank_ms", retrieved.Diagnostics.RerankMs,
		"memory_total_ms", retrieved.Diagnostics.TotalMs,
		"ms", retrieveMs,
		"reranker", retrieved.Diagnostics.Reranker,
		"model", retrieved.Diagnostics.RerankerModel,
	)
	if len(retrieved.Sources) == 0 {
		response := "No memory context found for that question."
		return chatOutcome{
			Response:   response,
			Tools:      []string{"memory.retrieve"},
			Path:       "memory_ask_empty",
			Transcript: appendAssistantMessage(effectiveMessages, response),
			History: historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Tools:     []string{"memory.retrieve"},
				Response:  response,
				Status:    "ok",
			},
		}, nil
	}

	prompt := buildMemoryAskPrompt(userQuery, retrieved.Sources)
	reasonStart := time.Now()
	var finalText string
	if s.callFinalMessage != nil {
		finalText, err = callFinalMessage(s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, callTimeout)
	} else {
		finalText, err = llm.CallOnceWithMaxTokens(s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, profile.maxTokens, callTimeout)
	}
	reasonMs := time.Since(reasonStart).Milliseconds()
	if err != nil {
		s.log().Error("orch.memory_ask.reason", "request_id", requestID, "session", req.GetSessionId(), "status", "error", "query", userQuery, "prompt_chars", len([]rune(prompt)), "ms", reasonMs, "err", err)
		return chatOutcome{History: historyEntry{
			Timestamp:  time.Now().Format(time.RFC3339),
			SessionID:  req.GetSessionId(),
			UserQuery:  userQuery,
			Tools:      []string{"memory.retrieve", "beemo.reason"},
			ToolResult: memoryAskDiagnostics(retrieved.Diagnostics),
			Status:     "error",
			Error:      fmt.Sprintf("llm_memory_answer: %v", err),
		}}, err
	}
	s.log().Info(
		"orch.memory_ask.complete",
		"request_id", requestID,
		"session", req.GetSessionId(),
		"status", "ok",
		"query", userQuery,
		"sources", len(retrieved.Sources),
		"prompt_chars", len([]rune(prompt)),
		"answer_chars", len([]rune(finalText)),
		"answer_profile", profile.name,
		"max_tokens", profile.maxTokens,
		"retrieve_ms", retrieveMs,
		"reason_ms", reasonMs,
		"total_ms", time.Since(start).Milliseconds(),
	)
	return chatOutcome{
		Response:   finalText,
		Tools:      []string{"memory.retrieve", "beemo.reason"},
		Path:       "memory_ask",
		Transcript: appendAssistantMessage(effectiveMessages, finalText),
		History: historyEntry{
			Timestamp:  time.Now().Format(time.RFC3339),
			SessionID:  req.GetSessionId(),
			UserQuery:  userQuery,
			Tools:      []string{"memory.retrieve", "beemo.reason"},
			ToolResult: memoryAskDiagnostics(retrieved.Diagnostics),
			Response:   finalText,
			Status:     "ok",
		},
	}, nil
}

type memoryAnswerProfile struct {
	name        string
	maxTokens   int
	sourceLimit int
}

func memoryAnswerProfileFor(question string) memoryAnswerProfile {
	query := strings.ToLower(strings.TrimSpace(question))
	expansive := []string{"describe", "explain", "compare", "contrast", "discuss", "summarize", "summary", "overview", "how does", "how do", "why does", "why do", "why is", "why are"}
	for _, cue := range expansive {
		if strings.Contains(query, cue) {
			return memoryAnswerProfile{name: "expansive", maxTokens: 384, sourceLimit: memoryAskSourceLimit}
		}
	}
	direct := []string{"who is", "who was", "whose ", "what is the name", "what was the name", "'s name", "when did", "when was", "where is", "where was", "how many", "which book", "which person"}
	for _, cue := range direct {
		if strings.Contains(query, cue) {
			return memoryAnswerProfile{name: "direct", maxTokens: 96, sourceLimit: 4}
		}
	}
	return memoryAnswerProfile{name: "standard", maxTokens: 192, sourceLimit: memoryAskSourceLimit}
}

func memoryAskRequested(options map[string]string) bool {
	value := strings.ToLower(strings.TrimSpace(options["memory_ask"]))
	return value == "1" || value == "true" || value == "yes"
}

func memoryAskOption(options map[string]string, key, fallback string) string {
	value := strings.TrimSpace(options[key])
	if value == "" || strings.EqualFold(value, "all") && (key == "memory_scope" || key == "memory_collection") {
		return fallback
	}
	return value
}

func fallbackRequestID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "session"
	}
	return fmt.Sprintf("%s-%d", sessionID, time.Now().UnixNano())
}

func buildMemoryAskPrompt(question string, sources []orchtools.MemoryRetrieveSource) string {
	parts := []string{
		"System:\nYou are Beemo. Answer only from the provided Memory Context. Read every numbered source before answering. Combine evidence across sources when needed, including aliases, pronouns, family relationships, and indirect references. First identify the subject of the user's question, then verify that any relationship evidence belongs to that subject. Do not answer with a relation for a different person unless the context links that person back to the asked subject. Cite only the bracketed source numbers shown before each source title, like [1] or [1][3]. If the context is insufficient, say what is missing. Match the answer depth to the question: answer direct factual questions in one or two sentences, but use several concise paragraphs when the user asks for an explanation, description, comparison, or synthesis.",
		"User:\nQuestion: " + question,
		"Memory Context:",
	}
	for index, source := range sources {
		parts = append(parts, fmt.Sprintf("[%d] %s\nUpdated: %s\nSource: %s\n%s", index+1, memoryAskTitle(source), shortMemoryAskDate(source.UpdatedAt), source.SourceURI, memoryAskEvidence(source)))
	}
	parts = append(parts, "Answer concisely with citations.")
	return strings.Join(parts, "\n\n")
}

func memoryAskTitle(source orchtools.MemoryRetrieveSource) string {
	title := strings.TrimSpace(source.Title)
	if title == "" {
		return "Untitled memory"
	}
	return title
}

func shortMemoryAskDate(value string) string {
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

func memoryAskEvidence(source orchtools.MemoryRetrieveSource) string {
	if evidence := strings.TrimSpace(source.EvidenceText); evidence != "" {
		return evidence
	}
	return truncateRunes(cleanMemoryAskText(source.Text), memoryAskSourceChars)
}

func cleanMemoryAskText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" || memoryAskChunkLabel(trimmed) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func memoryAskChunkLabel(line string) bool {
	value := strings.TrimSpace(strings.TrimPrefix(line, "Chunk "))
	if value == line || value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func memoryAskDiagnostics(diagnostics orchtools.MemoryRetrieveDiagnostics) string {
	return fmt.Sprintf(
		"status=%s candidates=%d rerank_candidates=%d selected=%d queries=%d reranker=%s model=%s reason=%s error=%s",
		diagnostics.Status,
		diagnostics.CandidateCount,
		diagnostics.RerankCandidateCount,
		diagnostics.SelectedCount,
		diagnostics.QueryCount,
		diagnostics.Reranker,
		diagnostics.RerankerModel,
		diagnostics.Reason,
		diagnostics.Error,
	)
}
