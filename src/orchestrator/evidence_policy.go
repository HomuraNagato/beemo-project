package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	pb "eve-beemo/proto/gen/proto"
)

type evidenceLevel string

const (
	evidenceNone      evidenceLevel = "none"
	evidencePreferred evidenceLevel = "preferred"
	evidenceRequired  evidenceLevel = "required"
)

type evidencePolicy struct {
	Level  evidenceLevel
	Tool   string
	Reason string
}

var citationPattern = regexp.MustCompile(`\[(\d+)]`)

func conversationEvidencePolicy(messages []*pb.ChatMessage) evidencePolicy {
	query := normalizedEvidenceText(latestUserQuery(messages))
	if query == "" {
		return evidencePolicy{Level: evidenceNone}
	}
	if containsAnyPhrase(query,
		"search the internet", "search online", "web search", "look up online", "lookup online",
		"verify online", "check online", "browse the web", "find sources", "cite sources",
	) {
		return requiredInternetEvidence("the user explicitly requested external verification")
	}
	if containsAnyPhrase(query,
		"still open", "open now", "currently open", "is it open", "are they open", "went out of business",
		"current price", "latest price", "in stock", "available now", "currently available",
		"latest version", "latest release", "most recent", "right now", "as of today",
		"street address", "what is the address for", "what's the address for", "where is it located",
	) || strings.Contains(query, "opening hours") || strings.Contains(query, "business hours") || isBusinessOpenQuestion(query) {
		return requiredInternetEvidence("the answer depends on current or mutable external facts")
	}
	if isLocalDiscoveryQuery(query) {
		return requiredInternetEvidence("the request is a location-dependent recommendation or lookup")
	}
	if hasLocalConversationContext(messages) && containsAnyPhrase(query,
		"address", "phone number", "contact information", "can't find", "cannot find", "couldn't find",
		"doesn't exist", "does not exist", "that is wrong", "that's wrong", "not correct", "incorrect",
		" closed", "not open",
	) {
		return requiredInternetEvidence("the user is checking or correcting a prior external claim")
	}
	if isRecommendationQuery(query) {
		return evidencePolicy{
			Level: evidencePreferred, Tool: "internet.search",
			Reason: "current external evidence would improve this recommendation",
		}
	}
	return evidencePolicy{Level: evidenceNone}
}

func requiredInternetEvidence(reason string) evidencePolicy {
	return evidencePolicy{Level: evidenceRequired, Tool: "internet.search", Reason: reason}
}

func normalizedEvidenceText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func containsAnyPhrase(value string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func isLocalDiscoveryQuery(query string) bool {
	location := containsAnyPhrase(query, " near ", " nearby", "near me", " around ")
	subject := containsAnyPhrase(query,
		"restaurant", "dinner", "lunch", "breakfast", "cafe", "coffee", "bar ", "hotel",
		"store", "shop", "doctor", "dentist", "service", "event", "place for", "place to",
	)
	return location && subject
}

func isRecommendationQuery(query string) bool {
	return containsAnyPhrase(query,
		"recommend", "recommendation", "suggest", "what is a good", "what's a good", "whats a good",
		"which is best", "what is the best", "what's the best", "good choice", "good option",
	)
}

func isBusinessOpenQuestion(query string) bool {
	if strings.Contains(query, "closed form") || strings.Contains(query, "open source") {
		return false
	}
	return (strings.HasPrefix(query, "is ") || strings.HasPrefix(query, "are ") || strings.Contains(query, "what time")) &&
		containsAnyPhrase(query, " open", " closed", " close", " opening")
}

func hasLocalConversationContext(messages []*pb.ChatMessage) bool {
	for index := len(messages) - 2; index >= 0 && index >= len(messages)-6; index-- {
		if messages[index] == nil {
			continue
		}
		text := normalizedEvidenceText(messages[index].GetContent())
		if isLocalDiscoveryQuery(text) || containsAnyPhrase(text,
			"restaurant", "cafe", "hotel", "store", "shop", "business", "address", "google maps",
		) {
			return true
		}
	}
	return false
}

func (policy evidencePolicy) promptText() string {
	switch policy.Level {
	case evidenceRequired:
		return fmt.Sprintf("required via %s because %s. A final answer will be rejected until this tool returns current evidence.", policy.Tool, policy.Reason)
	case evidencePreferred:
		return fmt.Sprintf("preferred via %s because %s. Use it when enabled; otherwise clearly qualify uncertainty.", policy.Tool, policy.Reason)
	default:
		return "none. Answer directly unless your own reasoning identifies a need for a tool."
	}
}

func (policy evidencePolicy) needsInternet(internetEnabled bool) bool {
	return policy.Tool == "internet.search" &&
		(policy.Level == evidenceRequired || policy.Level == evidencePreferred && internetEnabled)
}

func successfulInternetEvidence(observations []agentObservation) (int, bool) {
	sources := 0
	for _, observation := range observations {
		if observation.Tool != "internet.search" || observation.Error != "" || strings.TrimSpace(observation.Output) == "" {
			continue
		}
		sources += len(observation.Sources)
	}
	return sources, sources > 0
}

func hasValidInternetCitation(answer string, sourceCount int) bool {
	for _, match := range citationPattern.FindAllStringSubmatch(answer, -1) {
		value, err := strconv.Atoi(match[1])
		if err == nil && value >= 1 && value <= sourceCount {
			return true
		}
	}
	return false
}
