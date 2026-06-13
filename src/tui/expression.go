package main

import (
	"regexp"
	"strings"
)

type expression struct {
	Emotion string
	Label   string
	Face    []string
}

var expressionCatalog = map[string]expression{
	"neutral": {
		Emotion: "neutral",
		Label:   "steady",
		Face: []string{
			" .----.",
			"| o  o |",
			"|  --  |",
			" '----'",
		},
	},
	"happy": {
		Emotion: "happy",
		Label:   "warm",
		Face: []string{
			" .----.",
			"| ^  ^ |",
			"|  __  |",
			" '----'",
		},
	},
	"excited": {
		Emotion: "excited",
		Label:   "sparked",
		Face: []string{
			" .----.",
			"| >  < |",
			"|  \\_/ |",
			" '----'",
		},
	},
	"curious": {
		Emotion: "curious",
		Label:   "curious",
		Face: []string{
			" .----.",
			"| o  O |",
			"|  ?   |",
			" '----'",
		},
	},
	"thinking": {
		Emotion: "thinking",
		Label:   "thinking",
		Face: []string{
			" .----.",
			"| -  o |",
			"|  ..  |",
			" '----'",
		},
	},
	"concerned": {
		Emotion: "concerned",
		Label:   "careful",
		Face: []string{
			" .----.",
			"| o  o |",
			"|  /\\  |",
			" '----'",
		},
	},
	"sad": {
		Emotion: "sad",
		Label:   "soft",
		Face: []string{
			" .----.",
			"| .  . |",
			"|  __  |",
			" '----'",
		},
	},
	"surprised": {
		Emotion: "surprised",
		Label:   "surprised",
		Face: []string{
			" .----.",
			"| O  O |",
			"|  o   |",
			" '----'",
		},
	},
	"apologetic": {
		Emotion: "apologetic",
		Label:   "sorry",
		Face: []string{
			" .----.",
			"| u  u |",
			"|  --  |",
			" '----'",
		},
	},
	"sleepy": {
		Emotion: "sleepy",
		Label:   "sleepy",
		Face: []string{
			" .----.",
			"| -  - |",
			"|  _   |",
			" '----'",
		},
	},
	"error": {
		Emotion: "error",
		Label:   "fault",
		Face: []string{
			" .----.",
			"| x  x |",
			"|  !!  |",
			" '----'",
		},
	},
}

var expressionOrder = []string{
	"neutral",
	"happy",
	"excited",
	"curious",
	"thinking",
	"concerned",
	"sad",
	"surprised",
	"apologetic",
	"sleepy",
	"error",
}

var emotionTagPattern = regexp.MustCompile(`(?i)^\s*\[(?:emotion|expression|face):\s*([a-z_-]+)\]\s*`)

func defaultExpression() expression {
	return expressionCatalog["neutral"]
}

func expressionForEmotion(emotion string) expression {
	key := normalizeEmotion(emotion)
	if expr, ok := expressionCatalog[key]; ok {
		return expr
	}
	return defaultExpression()
}

func expressionForAssistantReply(reply string) expression {
	if tagged, ok := expressionTag(reply); ok {
		return expressionForEmotion(tagged)
	}

	text := strings.ToLower(reply)
	switch {
	case containsAny(text, "request failed", "error", "failed", "can't connect", "cannot connect"):
		return expressionForEmotion("error")
	case containsAny(text, "sorry", "apologize", "my mistake", "i was wrong"):
		return expressionForEmotion("apologetic")
	case containsAny(text, "i'm not sure", "i do not know", "i don't know", "missing", "need more", "clarify"):
		return expressionForEmotion("concerned")
	case containsAny(text, "?", "wonder", "curious", "what detail", "who is this about"):
		return expressionForEmotion("curious")
	case containsAny(text, "let me think", "thinking", "consider", "reason"):
		return expressionForEmotion("thinking")
	case containsAny(text, "great", "nice", "glad", "happy", "that works", "done"):
		return expressionForEmotion("happy")
	case containsAny(text, "wow", "amazing", "excited", "awesome", "excellent"):
		return expressionForEmotion("excited")
	case containsAny(text, "oh", "surprise", "unexpected"):
		return expressionForEmotion("surprised")
	case containsAny(text, "sad", "hurt", "upset", "unfortunately"):
		return expressionForEmotion("sad")
	case containsAny(text, "tired", "sleepy", "rest"):
		return expressionForEmotion("sleepy")
	default:
		return defaultExpression()
	}
}

func stripExpressionTag(reply string) string {
	return strings.TrimSpace(emotionTagPattern.ReplaceAllString(reply, ""))
}

func expressionTag(reply string) (string, bool) {
	match := emotionTagPattern.FindStringSubmatch(reply)
	if len(match) != 2 {
		return "", false
	}
	emotion := normalizeEmotion(match[1])
	if _, ok := expressionCatalog[emotion]; !ok {
		return "", false
	}
	return emotion, true
}

func normalizeEmotion(emotion string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(emotion, "_", "-")))
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
