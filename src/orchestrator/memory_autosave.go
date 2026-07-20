package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	orchtools "eve-beemo/src/orchestrator/tools"
)

func (s *orchestratorServer) maybeAutoSaveMemory(sessionID, userQuery string) {
	if !s.cfg.MemoryAutoSave || s.tools == nil || !looksLikeDurableMemory(userQuery) {
		return
	}
	args, err := json.Marshal(map[string]any{
		"text":        userQuery,
		"title":       memoryTitle(userQuery),
		"kind":        "fact",
		"source_type": "beemo",
		"source_uri":  "beemo://" + sessionID + "/" + time.Now().UTC().Format("20060102T150405.000000000Z"),
		"tags":        []string{"beemo", "auto"},
		"metadata": map[string]any{
			"session_id": sessionID,
			"auto_saved": true,
		},
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.MemoryTimeoutMs)*time.Millisecond)
	if s.cfg.MemoryTimeoutMs <= 0 {
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	}
	defer cancel()
	if _, err := s.tools.Execute(ctx, orchtools.Request{
		SessionID: sessionID,
		Action:    "memory.remember",
		Args:      args,
	}); err != nil {
		s.log().Warn("orch.memory.autosave", "session", sessionID, "status", "error", "err", err)
	}
}

func looksLikeDurableMemory(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" || len([]rune(normalized)) < 12 {
		return false
	}
	prefixes := []string{
		"remember ",
		"remember that ",
		"save this",
		"my ",
		"i live ",
		"i work ",
		"i prefer ",
		"i like ",
		"i don't like ",
		"i am ",
		"i'm ",
		"we are building ",
		"we're building ",
		"beemo should ",
		"starlight ",
		"memory palace ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return strings.Contains(normalized, " should remember ") ||
		strings.Contains(normalized, " to remember ") ||
		strings.Contains(normalized, " save to memory ")
}

func memoryTitle(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(strings.ToLower(text), "remember that ") {
		text = strings.TrimSpace(text[len("remember that "):])
	}
	runes := []rune(text)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return text
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
