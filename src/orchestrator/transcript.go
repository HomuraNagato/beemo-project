package main

import (
	"strings"

	pb "eve-beemo/proto/gen/proto"
)

func latestUserQuery(messages []*pb.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(messages[i].GetRole())) != "user" {
			continue
		}
		content := strings.TrimSpace(messages[i].GetContent())
		if content != "" {
			return content
		}
	}
	return ""
}

func cloneMessages(messages []*pb.ChatMessage) []*pb.ChatMessage {
	cloned := make([]*pb.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		cloned = append(cloned, &pb.ChatMessage{
			Role:    message.GetRole(),
			Content: message.GetContent(),
		})
	}
	return cloned
}

func normalizeMessages(messages []*pb.ChatMessage) []*pb.ChatMessage {
	normalized := make([]*pb.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.GetRole()))
		content := strings.TrimSpace(message.GetContent())
		if role == "" || content == "" {
			continue
		}
		normalized = append(normalized, &pb.ChatMessage{
			Role:    role,
			Content: content,
		})
	}
	return normalized
}

func trimMessages(messages []*pb.ChatMessage) []*pb.ChatMessage {
	normalized := normalizeMessages(messages)
	if len(normalized) <= sessionTranscriptMessages {
		return normalized
	}
	return cloneMessages(normalized[len(normalized)-sessionTranscriptMessages:])
}

func appendAssistantMessage(messages []*pb.ChatMessage, response string) []*pb.ChatMessage {
	text := strings.TrimSpace(response)
	if text == "" {
		return trimMessages(messages)
	}
	next := cloneMessages(messages)
	next = append(next, &pb.ChatMessage{Role: "assistant", Content: text})
	return trimMessages(next)
}

func requestSuppliesTranscript(messages []*pb.ChatMessage) bool {
	if len(messages) > 1 {
		return true
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(message.GetRole())) != "user" {
			return true
		}
	}
	return false
}

func (s *orchestratorServer) getTranscript(sessionID string) []*pb.ChatMessage {
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if s.transcriptBySession == nil {
		return nil
	}
	return cloneMessages(s.transcriptBySession[sessionID])
}

func (s *orchestratorServer) setTranscript(sessionID string, messages []*pb.ChatMessage) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	trimmed := trimMessages(messages)
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if len(trimmed) == 0 {
		if s.transcriptBySession != nil {
			delete(s.transcriptBySession, sessionID)
		}
		return
	}
	if s.transcriptBySession == nil {
		s.transcriptBySession = make(map[string][]*pb.ChatMessage)
	}
	s.transcriptBySession[sessionID] = trimmed
}

func (s *orchestratorServer) resolveMessages(sessionID string, incoming []*pb.ChatMessage) []*pb.ChatMessage {
	normalized := normalizeMessages(incoming)
	if len(normalized) == 0 {
		return nil
	}
	if requestSuppliesTranscript(normalized) {
		return trimMessages(normalized)
	}
	stored := s.getTranscript(sessionID)
	if len(stored) == 0 {
		return trimMessages(normalized)
	}
	combined := cloneMessages(stored)
	combined = append(combined, cloneMessages(normalized)...)
	return trimMessages(combined)
}
