package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
)

func parseToolCalls(text string) ([]toolCall, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}
	var calls []toolCall
	if err := json.Unmarshal([]byte(trimmed), &calls); err != nil {
		return nil, err
	}
	for i := range calls {
		if strings.TrimSpace(calls[i].Tool) == "" {
			return nil, fmt.Errorf("tool call %d missing tool name", i)
		}
		if len(calls[i].Args) == 0 {
			calls[i].Args = json.RawMessage(`{}`)
		}
		if err := validateArgsObject(calls[i].Args); err != nil {
			return nil, fmt.Errorf("tool call %d: %w", i, err)
		}
	}
	return calls, nil
}

func supportedTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "get_time", "weather", "older_sister", "calculator", "memory.search", "memory.remember", "beemo.direct":
		return true
	default:
		return false
	}
}

func filterSupportedToolCalls(calls []toolCall) []toolCall {
	filtered := calls[:0]
	for _, call := range calls {
		if supportedTool(call.Tool) {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

func toolNames(calls []toolCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.Tool)
	}
	return names
}

func validateToolCall(candidates []routing.Candidate, routes []routing.Route, call toolCall) error {
	if !supportedTool(call.Tool) {
		return fmt.Errorf("unsupported tool action: %s", call.Tool)
	}
	if err := validateArgsObject(call.Args); err != nil {
		return err
	}
	route, matched, err := routing.MatchCall(candidates, routes, call.Tool, call.Args)
	if err != nil {
		return err
	}
	if matched && strings.TrimSpace(route.Handler.Target) != "" && strings.TrimSpace(route.Handler.Target) != call.Tool {
		return fmt.Errorf("tool %q does not match route %q target %q", call.Tool, route.ID, route.Handler.Target)
	}
	return nil
}

func requireRouteToolCallGrammar(grammar string, route routing.Route) string {
	grammar = requireSingleToolCallGrammar(grammar)
	target := strings.TrimSpace(route.Handler.Target)
	switch target {
	case "get_time":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= get_time_call")
	case "weather":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= weather_call")
	case "older_sister":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= older_sister_call")
	case "calculator":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= calculator_call")
		grammar = restrictCalculatorGrammar(grammar, route)
	case "memory.search":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= memory_search_call")
	case "memory.remember":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= memory_remember_call")
	case "beemo.direct":
		grammar = replaceBaseToolCallRule(grammar, "tool_call ::= beemo_direct_call")
	}
	return grammar
}

const baseToolCallRule = "tool_call ::= get_time_call | weather_call | older_sister_call | calculator_call | memory_search_call | memory_remember_call | beemo_direct_call"
const legacyBaseToolCallRule = "tool_call ::= get_time_call | weather_call | older_sister_call | calculator_call | beemo_direct_call"

func replaceBaseToolCallRule(grammar string, replacement string) string {
	if strings.Contains(grammar, baseToolCallRule) {
		return strings.Replace(grammar, baseToolCallRule, replacement, 1)
	}
	return strings.Replace(grammar, legacyBaseToolCallRule, replacement, 1)
}

func restrictCalculatorGrammar(grammar string, route routing.Route) string {
	operation := strings.TrimSpace(fmt.Sprint(route.DefaultArgs["operation"]))
	if operation == "" {
		return grammar
	}
	ruleByOperation := map[string]string{
		"expression":     "expression_args",
		"convert":        "convert_args",
		"bmi":            "bmi_args",
		"bmr":            "bmr_args",
		"tdee":           "tdee_args",
		"percent_of":     "percent_of_args",
		"percent_change": "percent_change_args",
		"percent_ratio":  "percent_ratio_args",
	}
	rule := ruleByOperation[operation]
	if rule == "" {
		return grammar
	}
	return strings.Replace(grammar, "calc_args ::= expression_args | convert_args | bmi_args | bmr_args | tdee_args | percent_of_args | percent_change_args | percent_ratio_args", "calc_args ::= "+rule, 1)
}

func validateArgsObject(args json.RawMessage) error {
	if len(args) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return fmt.Errorf("invalid tool args: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("tool args must be a JSON object")
	}
	return nil
}

func fromPlannedCall(call orchtools.PlannedCall) toolCall {
	return toolCall{Tool: call.Action, Args: call.Args}
}

func toPlannedCall(call toolCall) orchtools.PlannedCall {
	return orchtools.PlannedCall{Action: call.Tool, Args: call.Args}
}

func readGrammarFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
