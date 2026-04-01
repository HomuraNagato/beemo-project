package prompts

import "fmt"

func ToolDecision(userQuery string) string {
	return "Decide which tools to use. Valid tools: get_time. Respond with a bracketed list only, e.g. [get_time] or []. Do not include any other text.\nUser query: " + userQuery + "\nTools:"
}

func FinalResponse(userQuery, decision, toolResult string) string {
	return fmt.Sprintf(
		"Answer the user using ONLY the provided context. If Tool result is present, you MUST use it verbatim for any factual claims and must not invent facts. If Tool result is empty, answer only from the user query and decision.\nUser query: %s\nDecision: %s\nTool result: %s\nProvide a concise answer.",
		userQuery,
		decision,
		toolResult,
	)
}
