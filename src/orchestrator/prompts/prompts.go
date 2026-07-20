package prompts

import (
	"fmt"
	"strings"
)

func ToolDecision(userQuery, activeTranscript string) string {
	return `Choose at most one tool. Return JSON array only, or [].
Tools: get_time(time/date), weather(forecast/current conditions), calculator(math/conversion/BMI/BMR/TDEE), memory.search(personal/local memory), memory.remember(save durable user memory), beemo.direct(local direct response).
Rules:
- time/date/day/month/year/today/tomorrow/yesterday -> get_time.
- weather/rain/temperature/forecast -> weather; include location, when, hour_local, focus if explicit.
- ask what you remember, personal history, prior life details, saved notes, projects, preferences, people, places, or "my ..." facts from memory -> memory.search with query.
- explicit "remember this" or "save this to memory" -> memory.remember with text.
- math, unit conversion, percent, BMI/BMR/TDEE -> calculator; include explicit values only, do not convert or duplicate measurements.
- casual conversation, brainstorming, simple local response, or no specialized tool needed -> beemo.direct.
- Use the active thread for immediate follow-ups only. Do not invent missing facts.
- If required fields are missing, omit them. Do not answer.
Examples: "what time is it?"=>[{"tool":"get_time","args":{}}]; "what is 20% of 85?"=>[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]; "what is the weather in Tokyo tomorrow?"=>[{"tool":"weather","args":{"location":"Tokyo","when":"tomorrow"}}].

Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func RoutedToolDecision(userQuery, activeTranscript, routeCandidates string) string {
	return `Choose exactly one candidate route. Return JSON array only.
Rules: use ONLY candidate routes; prefer the highest similarity route unless the user query clearly contradicts it; choose beemo.direct when direct local response is the best candidate; preserve default_args; omit unknown required fields; copy explicit measurements exactly; use active thread for immediate follow-ups; do not answer.

Candidate routes:
` + transcriptBlock(routeCandidates) + `

Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func RouteDecision(userQuery, activeTranscript, routeCandidates string) string {
	return `Choose exactly one route_id from the candidate routes. Return JSON object only: {"route_id":"..."}.
Rules:
- Choose intent only. Do not write tool arguments.
- Prefer the highest similarity route unless the user query clearly contradicts it.
- calculator.bmi, calculator.bmr, and calculator.tdee require the user to explicitly ask for that metric by name.
- Use memory.answer for questions answered by indexed local memories, notes, books, textbooks, PDFs, EPUBs, or documents, especially when passages must be retrieved and combined.
- Use memory.search for requests to list or inspect saved personal memories and prior notes.
- Use memory.remember only when the user explicitly asks to remember or save something.
- Use beemo.direct only when no specialized tool or external help is needed.

Candidate routes:
` + transcriptBlock(routeCandidates) + `

Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Route decision:`
}

func RouteToolExtraction(userQuery, activeTranscript, selectedRoute string) string {
	return `Generate the single tool call for the selected route. Return JSON array only.
Rules:
- Use only the selected route.
- Preserve default_args exactly.
- Include only facts explicitly present in the user query or active thread.
- Copy measurements exactly; do not convert, duplicate, infer, or invent measurements.
- Omit missing fields rather than guessing.
- Do not answer the user.

Selected route:
` + transcriptBlock(selectedRoute) + `

Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func RetryToolDecision(userQuery, activeTranscript string) string {
	return `Re-check tool choice after previous []. Return one JSON tool call or [].
Tools: get_time, weather, calculator, memory.search, memory.remember, beemo.direct.
Rules: time/date=>get_time; weather=>weather; personal saved/local memory=>memory.search; explicit remember/save=>memory.remember; math/conversion/BMI/BMR/TDEE=>calculator; casual/direct local response=>beemo.direct. Use active thread for immediate follow-ups, omit missing fields, do not answer.

Previous answer: []
Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func FinalResponse(originalUserQuery, latestUserReply, activeTranscript, decision, toolResult string) string {
	return fmt.Sprintf(
		"Answer using only this context. Treat Tool result as factual source data. Preserve the exact quantities, units, and labels from Tool result; do not reinterpret, correct, or rename them from the user query. Convert raw values into a natural, concise answer only when that does not change the meaning. For ISO timestamps, state the time/date in plain language. Do not invent facts. Never write placeholders such as [ISO timestamp]. If Tool result is empty, answer directly only when Decision is beemo.direct; otherwise say the needed tool did not return a result.\nOriginal user query: %s\nLatest user reply: %s\nActive conversation thread:\n%s\nDecision: %s\nTool result: %s\nConcise answer:",
		originalUserQuery,
		latestUserReply,
		transcriptBlock(activeTranscript),
		decision,
		toolResult,
	)
}

func ResumeToolUpdate(originalUserQuery, activeTranscript, toolName, currentArgs string, missing []string, question, latestUserReply string) string {
	return fmt.Sprintf(
		`Resume the pending tool call.
Return JSON array only: [] or one updated %s call.
Rules: preserve known args and calculator operation; fill only missing/supported fields; copy measurements exactly; if latest reply is unrelated, return []; do not answer.

Original user query: %s
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
