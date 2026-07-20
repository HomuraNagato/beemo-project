package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"eve-beemo/src/orchestrator/routing"
)

type routeDecision struct {
	RouteID string `json:"route_id"`
}

const routeDecisionGrammar = `root ::= "{" ws "\"route_id\"" ws ":" ws route_id ws "}"
route_id ::= "\"" route_id_chars "\""
route_id_chars ::= route_id_char+
route_id_char ::= [a-zA-Z0-9_.-]
ws ::= ""`

func parseRouteDecision(text string, candidates []routing.Candidate) (routing.Candidate, error) {
	var decision routeDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decision); err != nil {
		return routing.Candidate{}, fmt.Errorf("invalid route decision: %w", err)
	}
	routeID := strings.TrimSpace(decision.RouteID)
	if routeID == "" {
		return routing.Candidate{}, fmt.Errorf("route decision missing route_id")
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Route.ID) == routeID {
			return candidate, nil
		}
	}
	return routing.Candidate{}, fmt.Errorf("route decision %q is not in candidate set", routeID)
}

func validateSelectedRoute(candidate routing.Candidate, userQuery string) error {
	routeID := strings.TrimSpace(candidate.Route.ID)
	query := strings.ToLower(userQuery)
	switch routeID {
	case "calculator.bmi":
		if !containsWord(query, "bmi") && !strings.Contains(query, "body mass index") {
			return fmt.Errorf("route %s requires explicit BMI intent", routeID)
		}
	case "calculator.bmr":
		if !containsWord(query, "bmr") && !strings.Contains(query, "basal metabolic") {
			return fmt.Errorf("route %s requires explicit BMR intent", routeID)
		}
	case "calculator.tdee":
		if !containsWord(query, "tdee") && !strings.Contains(query, "total daily energy") {
			return fmt.Errorf("route %s requires explicit TDEE intent", routeID)
		}
	}
	return nil
}

func containsWord(text, word string) bool {
	if text == "" || word == "" {
		return false
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	return re.MatchString(text)
}

func toolCallFromRoute(candidate routing.Candidate) (toolCall, bool, error) {
	route := candidate.Route
	target := strings.TrimSpace(route.Handler.Target)
	if target == "" || !supportedTool(target) {
		return toolCall{}, false, nil
	}
	args := route.DefaultArgs
	if args == nil {
		args = map[string]any{}
	}
	switch target {
	case "get_time", "beemo.direct":
		raw, err := json.Marshal(args)
		if err != nil {
			return toolCall{}, false, err
		}
		return toolCall{Tool: target, Args: raw}, true, nil
	default:
		return toolCall{}, false, nil
	}
}

func isMemoryAnswerRoute(candidate routing.Candidate) bool {
	return strings.TrimSpace(candidate.Route.Handler.Target) == "memory.answer"
}

func selectedRouteBlock(candidate routing.Candidate) string {
	return routing.FormatCandidates([]routing.Candidate{candidate})
}
