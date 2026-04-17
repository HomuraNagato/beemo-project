package chatctx

import (
	"fmt"
	"regexp"
	"strings"

	pb "eve-beemo/proto/gen/proto"
)

type ActiveContext struct {
	Transcript   string
	UserEvidence string
	Messages     []*pb.ChatMessage
}

type turn struct {
	messages      []*pb.ChatMessage
	userText      string
	anchors       map[string]struct{}
	briefFollowUp bool
}

type thread struct {
	turns   []turn
	anchors map[string]struct{}
}

const maxPromptChars = 280

var (
	tokenPattern = regexp.MustCompile(`[A-Za-z0-9_./:-]+`)
	stopwords    = map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "be": {}, "can": {}, "could": {},
		"date": {}, "day": {}, "debug": {}, "do": {}, "does": {}, "error": {}, "failing": {},
		"fail": {}, "fix": {}, "for": {}, "from": {}, "help": {}, "how": {}, "i": {},
		"in": {}, "is": {}, "issue": {}, "it": {}, "me": {}, "my": {}, "need": {}, "now": {},
		"of": {}, "on": {}, "problem": {},
		"or": {}, "please": {}, "should": {}, "tell": {}, "that": {}, "the": {}, "this": {},
		"time": {}, "to": {}, "today": {}, "tomorrow": {}, "we": {}, "what": {}, "when": {},
		"where": {}, "which": {}, "who": {}, "why": {}, "would": {}, "year": {}, "yesterday": {},
		"you": {}, "your": {},
	}
	followUpPrefixes = []string{
		"what about",
		"how about",
		"and ",
		"also ",
		"same ",
		"then ",
		"for that",
		"for this",
		"what if",
	}
)

func Build(messages []*pb.ChatMessage, maxMessages, maxTurns int) ActiveContext {
	truncated := tailMessages(messages, maxMessages)
	turns := buildTurns(truncated)
	if len(turns) == 0 {
		return ActiveContext{}
	}

	active := selectActiveThread(turns)
	if maxTurns > 0 && len(active.turns) > maxTurns {
		active.turns = active.turns[len(active.turns)-maxTurns:]
	}

	return ActiveContext{
		Transcript:   formatTurns(active.turns, false),
		UserEvidence: formatTurns(active.turns, true),
		Messages:     flattenMessages(active.turns),
	}
}

func tailMessages(messages []*pb.ChatMessage, maxMessages int) []*pb.ChatMessage {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return messages
	}
	return messages[len(messages)-maxMessages:]
}

func buildTurns(messages []*pb.ChatMessage) []turn {
	turns := make([]turn, 0, len(messages))
	var current *turn

	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.GetRole()))
		content := strings.TrimSpace(message.GetContent())
		if role == "" || content == "" {
			continue
		}

		if role == "user" {
			if current != nil {
				turns = append(turns, *current)
			}
			anchors := extractAnchors(content)
			current = &turn{
				messages:      []*pb.ChatMessage{message},
				userText:      content,
				anchors:       anchors,
				briefFollowUp: isBriefFollowUp(content, anchors),
			}
			continue
		}

		if current != nil && (role == "assistant" || role == "system") {
			current.messages = append(current.messages, message)
		}
	}

	if current != nil {
		turns = append(turns, *current)
	}
	return turns
}

func selectActiveThread(turns []turn) thread {
	active := newThread(turns[0])
	for _, candidate := range turns[1:] {
		if shouldStartNewThread(active, candidate) {
			active = newThread(candidate)
			continue
		}
		active.turns = append(active.turns, candidate)
		for anchor := range candidate.anchors {
			active.anchors[anchor] = struct{}{}
		}
	}
	return active
}

func newThread(first turn) thread {
	anchors := make(map[string]struct{}, len(first.anchors))
	for anchor := range first.anchors {
		anchors[anchor] = struct{}{}
	}
	return thread{
		turns:   []turn{first},
		anchors: anchors,
	}
}

func shouldStartNewThread(active thread, candidate turn) bool {
	if candidate.briefFollowUp {
		return false
	}
	if followsAssistantQuestion(active) && looksLikeAnswer(candidate.userText) {
		return false
	}
	if len(candidate.anchors) == 0 {
		return false
	}

	overlap := anchorOverlap(active.anchors, candidate.anchors)
	if overlap >= 2 {
		return false
	}

	newAnchors := 0
	strongNewAnchors := 0
	for anchor := range candidate.anchors {
		if _, ok := active.anchors[anchor]; ok {
			continue
		}
		newAnchors++
		if isStrongAnchor(anchor) {
			strongNewAnchors++
		}
	}

	switch {
	case strongNewAnchors >= 1 && overlap == 0:
		return true
	case strongNewAnchors >= 2 && overlap <= 1:
		return true
	case newAnchors >= 3 && overlap == 0:
		return true
	default:
		return false
	}
}

func followsAssistantQuestion(active thread) bool {
	if len(active.turns) == 0 {
		return false
	}
	lastTurn := active.turns[len(active.turns)-1]
	if len(lastTurn.messages) == 0 {
		return false
	}
	lastMessage := lastTurn.messages[len(lastTurn.messages)-1]
	role := strings.ToLower(strings.TrimSpace(lastMessage.GetRole()))
	content := strings.TrimSpace(lastMessage.GetContent())
	return role == "assistant" && strings.HasSuffix(content, "?")
}

func looksLikeAnswer(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || strings.Contains(lower, "?") {
		return false
	}
	words := strings.Fields(lower)
	if len(words) == 0 || len(words) > 8 {
		return false
	}
	switch words[0] {
	case "what", "how", "can", "could", "would", "please", "help", "need", "want", "explain", "write", "create", "debug", "fix", "show", "tell", "now":
		return false
	default:
		return true
	}
}

func extractAnchors(text string) map[string]struct{} {
	matches := tokenPattern.FindAllString(text, -1)
	anchors := make(map[string]struct{}, len(matches))
	for _, raw := range matches {
		token := strings.ToLower(strings.Trim(raw, " \t\n\r.,!?\"'`()[]{}"))
		if token == "" {
			continue
		}
		if _, stop := stopwords[token]; stop {
			continue
		}
		if !looksUsefulAnchor(raw, token) {
			continue
		}
		anchors[token] = struct{}{}
	}
	return anchors
}

func looksUsefulAnchor(raw, token string) bool {
	if len(token) < 2 {
		return false
	}
	if strings.ContainsAny(raw, "0123456789_./:-") {
		return true
	}
	return len(token) >= 3
}

func isStrongAnchor(anchor string) bool {
	if strings.ContainsAny(anchor, "0123456789_./:-") {
		return true
	}
	return false
}

func isBriefFollowUp(text string, anchors map[string]struct{}) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range followUpPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for anchor := range anchors {
		if isStrongAnchor(anchor) {
			return false
		}
	}
	words := strings.Fields(lower)
	return len(words) <= 6 && len(anchors) <= 2
}

func anchorOverlap(left, right map[string]struct{}) int {
	count := 0
	for anchor := range left {
		if _, ok := right[anchor]; ok {
			count++
		}
	}
	return count
}

func formatTurns(turns []turn, userOnly bool) string {
	lines := make([]string, 0, len(turns)*2)
	for _, turn := range turns {
		for _, message := range turn.messages {
			role := strings.ToLower(strings.TrimSpace(message.GetRole()))
			if role == "" {
				continue
			}
			if userOnly && role != "user" {
				continue
			}
			content := compactText(message.GetContent())
			if content == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %s", role, content))
		}
	}
	return strings.Join(lines, "\n")
}

func compactText(text string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(normalized) <= maxPromptChars {
		return normalized
	}
	return normalized[:maxPromptChars] + "..."
}

func flattenMessages(turns []turn) []*pb.ChatMessage {
	flattened := make([]*pb.ChatMessage, 0, len(turns)*2)
	for _, turn := range turns {
		for _, message := range turn.messages {
			if message == nil {
				continue
			}
			flattened = append(flattened, &pb.ChatMessage{
				Role:    message.GetRole(),
				Content: message.GetContent(),
			})
		}
	}
	return flattened
}
