package prompts

import (
	"fmt"
	"strings"
)

func ToolDecision(userQuery, activeTranscript, subjectContext string) string {
	return `Choose at most one tool. Return JSON array only, or [].
Tools: get_time(time/date), weather(forecast/current conditions), older_sister(web/current/external help), calculator(math/conversion/BMI/BMR/TDEE), memory_lookup(recall stored facts).
Rules:
- time/date/day/month/year/today/tomorrow/yesterday -> get_time.
- weather/rain/temperature/forecast -> weather; include location, when, hour_local, focus if explicit.
- ask ChatGPT/older sister/search/look up/verify -> older_sister with query and web_search when useful.
- recall remembered facts -> memory_lookup; args can be {} if attribute is unclear.
- math, unit conversion, percent, BMI/BMR/TDEE -> calculator; include explicit values only, do not convert or duplicate measurements.
- Use subject context for my/her/his/names/relationships and active thread for follow-ups.
- If required fields are missing, omit them. Do not answer.
Examples: "what time is it?"=>[{"tool":"get_time","args":{}}]; "what is 20% of 85?"=>[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]; "what is my height?"=>[{"tool":"memory_lookup","args":{}}]; "what is the weather in Tokyo tomorrow?"=>[{"tool":"weather","args":{"location":"Tokyo","when":"tomorrow"}}].

Resolved subject context:
` + subjectContextBlock(subjectContext) + `
Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func RoutedToolDecision(userQuery, activeTranscript, subjectContext, routeCandidates string) string {
	return `Choose one candidate route. Return JSON array only, or [].
Rules: use ONLY candidate routes; preserve default_args; omit unknown required fields; copy explicit measurements exactly; use subject context and active thread for references/follow-ups; do not answer.

Candidate routes:
` + transcriptBlock(routeCandidates) + `

Resolved subject context:
` + subjectContextBlock(subjectContext) + `
Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func RetryToolDecision(userQuery, activeTranscript, subjectContext string) string {
	return `Re-check tool choice after previous []. Return one JSON tool call or [].
Tools: get_time, weather, older_sister, calculator, memory_lookup.
Rules: time/date=>get_time; weather=>weather; ask/search/verify=>older_sister; recall facts=>memory_lookup; math/conversion/BMI/BMR/TDEE=>calculator. Use context for follow-ups, omit missing fields, do not answer.

Previous answer: []
Resolved subject context:
` + subjectContextBlock(subjectContext) + `
Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func FinalResponse(originalUserQuery, latestUserReply, activeTranscript, subjectContext, decision, toolResult string) string {
	return fmt.Sprintf(
		"Answer using only this context. Use Tool result verbatim for factual claims. If Tool result has resolved_subject, name that subject. Do not invent facts or volunteer unrelated memories. If Tool result is empty, do not guess.\nOriginal user query: %s\nLatest user reply: %s\nResolved subject context:\n%s\nActive conversation thread:\n%s\nDecision: %s\nTool result: %s\nConcise answer:",
		originalUserQuery,
		latestUserReply,
		subjectContextBlock(subjectContext),
		transcriptBlock(activeTranscript),
		decision,
		toolResult,
	)
}

func ResumeToolUpdate(originalUserQuery, activeTranscript, subjectContext, toolName, currentArgs string, missing []string, question, latestUserReply string) string {
	return fmt.Sprintf(
		`Resume the pending tool call.
Return JSON array only: [] or one updated %s call.
Rules: preserve known args and calculator operation; fill only missing/supported fields; copy measurements exactly; use context for references; if latest reply is unrelated, return []; do not answer.

Original user query: %s
Resolved subject context:
%s
Active conversation thread:
%s
Pending tool: %s
Current structured args: %s
Missing fields: %s
Question asked: %s
Latest user reply: %s

Updated tool call:`,
		toolName,
		originalUserQuery,
		subjectContextBlock(subjectContext),
		transcriptBlock(activeTranscript),
		toolName,
		currentArgs,
		strings.Join(missing, ", "),
		question,
		latestUserReply,
	)
}

func transcriptBlock(recentTranscript string) string {
	trimmed := strings.TrimSpace(recentTranscript)
	if trimmed == "" {
		return "(none)"
	}
	return trimmed
}

func subjectContextBlock(subjectContext string) string {
	trimmed := strings.TrimSpace(subjectContext)
	if trimmed == "" {
		return "(none)"
	}
	return trimmed
}
