package prompts

import (
	"fmt"
	"strings"
)

func ToolDecision(userQuery, activeTranscript, subjectContext string) string {
	return `Decide which tools to use.
Valid tools:
- get_time: use for current or relative time/date questions
- calculator: use for arithmetic, health calculations, and supported unit conversions
- memory_lookup: use for direct recall of a previously stated subject fact or remembered detail such as weight, height, age, gender, activity level, birthday, or a start date

Return JSON array only. Each item must be:
- "tool": tool name
- "args": object of structured arguments

Return at most one tool call. Do not list alternatives.
Return [] only when neither tool can help.

Examples:
- [{"tool":"get_time","args":{}}]
- User query "what time is it?" -> [{"tool":"get_time","args":{}}]
- User query "what date is it today?" -> [{"tool":"get_time","args":{}}]
- User query "what day is tomorrow?" -> [{"tool":"get_time","args":{}}]
- User query "what month is it?" -> [{"tool":"get_time","args":{}}]
- User query "what is five days from today?" -> [{"tool":"get_time","args":{}}]
- User query "what is my weight?" -> [{"tool":"memory_lookup","args":{}}]
- User query "what is her height?" -> [{"tool":"memory_lookup","args":{}}]
- User query "when did i start my new job?" -> [{"tool":"memory_lookup","args":{}}]
- [{"tool":"calculator","args":{"operation":"expression","expression":"12 / (3 + 1)"}}]
- [{"tool":"calculator","args":{"operation":"convert","input":[{"unit":"ft","value":5},{"unit":"in","value":4}],"to_unit":"cm"}}]
- [{"tool":"calculator","args":{"operation":"convert","value":10,"from_unit":"mi/hr","to_unit":"min/mi"}}]
- [{"tool":"calculator","args":{"operation":"convert","value":5,"from_unit":"mg/ml","to_unit":"g/l"}}]
- [{"tool":"calculator","args":{"operation":"bmi"}}]
- [{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"lb","value":101}],"height":[{"unit":"ft","value":5},{"unit":"in","value":4}]}}]
- [{"tool":"calculator","args":{"operation":"bmr","age_years":34,"gender":"female","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]
- [{"tool":"calculator","args":{"operation":"tdee","age_years":34,"gender":"female","activity_level":"moderate","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]
- Active thread has user weight/height/age/gender for BMR, then user asks "what is the tdee?" -> reuse those explicit values in the tdee call and omit only activity_level if it was never provided
- [{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]
- User query "summarize this paragraph" -> []

Rules:
- For current or relative time/date questions, including time, day, date, month, year, today, tomorrow, yesterday, or "X days from now/today", use get_time.
- A question about the current or relative date/time should never return [].
- For direct recall of a previously stated subject fact or remembered detail, use memory_lookup rather than calculator.
- For direct recall, args may be empty. Do not guess or invent an attribute when the request is ambiguous.
- For math, BMI/BMR/TDEE, pace/speed, chemistry-style unit conversions, or other unit conversion questions, use calculator.
- If the user explicitly asks for BMI, BMR, or TDEE, use calculator with that operation even when some fields are missing.
- For follow-up BMI, BMR, or TDEE questions, carry forward explicit measurements or demographics from the active conversation thread unless the user corrected them later in that thread.
- For BMI, BMR, or TDEE, copy measurements exactly as the user stated them. Do not convert, normalize, or duplicate weight or height values.
- Use multi-component measurements only when the user explicitly gave a composite value like 5 ft 4 in.
- Use the resolved subject context for references like "my brother", "Mark", or "his". Prefer explicit subject links over guesses.
- Use calculator convert for both simple units and compound units like mi/hr, min/mi, mg/ml, or g/l.
- Use the active conversation thread to resolve follow-up references such as "what about tomorrow?", "same conversion", or "what about bmr?".
- Do not answer the user.
- Return valid JSON only.
- If required information is missing, omit the missing fields rather than guessing.

Resolved subject context:
` + subjectContextBlock(subjectContext) + `
Active conversation thread:
` + transcriptBlock(activeTranscript) + `
User query: ` + userQuery + `
Tool calls:`
}

func RoutedToolDecision(userQuery, activeTranscript, subjectContext, routeCandidates string) string {
	return `Decide which candidate route to use.
You are given a short list of candidate routes that were selected by semantic retrieval.
Choose the best matching route, then return JSON array only with at most one tool call.

Return JSON array only. Each item must be:
- "tool": tool name
- "args": object of structured arguments

Return [] only when none of the candidate routes actually fit the user request.
Do not list alternatives. Do not answer the user.

Important:
- Use ONLY the candidate routes below.
- Prefer the best matching route from the candidates rather than inventing a new route.
- If a route includes default_args, preserve them unless the user explicitly provides supported values for additional fields.
- If required information is missing, omit the missing fields rather than guessing.
- For direct recall routes such as stored weight, height, birthday, or a remembered date/event, use memory_lookup instead of calculator.
- For direct recall, args may be empty. Do not guess or invent an attribute when the request is ambiguous.
- For follow-up requests, use the active conversation thread to resolve references such as "what about tomorrow?" or "what about bmr?".
- For BMI, BMR, or TDEE follow-ups, carry forward explicit measurements or demographics from the active conversation thread when available.
- For BMI, BMR, or TDEE, copy measurements exactly as the user stated them. Do not convert, normalize, or duplicate weight or height values.
- Use multi-component measurements only when the user explicitly gave a composite value like 5 ft 4 in.
- Use the resolved subject context for references like "my brother", "Mark", or "his". Prefer explicit subject links over guesses.
- Return valid JSON only.

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
	return `Re-check the user's request and choose a tool only if one can help.
Valid tools:
- get_time: use for current or relative time/date questions
- calculator: use for arithmetic, health calculations, and supported unit conversions
- memory_lookup: use for direct recall of a previously stated subject fact or remembered detail such as weight, height, age, gender, activity level, birthday, or a start date

Return JSON array only. Return at most one tool call.
Return [] only when neither tool applies.

Important:
- If the user asks about current or relative time/date/day/month/year, return [{"tool":"get_time","args":{}}], not [].
- If the user asks to recall a known subject fact or remembered detail like weight, height, age, gender, activity level, birthday, or a start date, return memory_lookup.
- If the user asks for math, unit conversion, BMI, BMR, TDEE, pace, speed, or percentages, return calculator.
- For follow-up BMI, BMR, or TDEE questions, reuse explicit measurements or demographics from the active conversation thread and omit only fields that are still missing.
- For BMI, BMR, or TDEE, copy measurements exactly as the user stated them. Do not convert, normalize, or duplicate weight or height values.
- Use multi-component measurements only when the user explicitly gave a composite value like 5 ft 4 in.
- Use the active conversation thread to resolve follow-up references such as "what about tomorrow?" or "what about bmr?".
- Use the resolved subject context for references like "my brother", "Mark", or "his". Prefer explicit subject links over guesses.
- Do not answer the user.

Examples:
- User query "what time is it?" -> [{"tool":"get_time","args":{}}]
- User query "what date will it be 5 days from today?" -> [{"tool":"get_time","args":{}}]
- User query "what day is tomorrow?" -> [{"tool":"get_time","args":{}}]
- User query "what is my height?" -> [{"tool":"memory_lookup","args":{}}]
- User query "when did i start my new job?" -> [{"tool":"memory_lookup","args":{}}]
- User query "what is 20% of 85?" -> [{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]
- User query "summarize this paragraph" -> []

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
		"Answer the user using ONLY the provided context. Use the active conversation thread and resolved subject context to interpret follow-up references, but do not invent facts. If Tool result is present, you MUST use it verbatim for any factual claims. If Tool result is empty, do not guess missing facts. If the question depends on the current or relative date/time and Tool result is empty, say you need the current time/date context rather than guessing.\nOriginal user query: %s\nLatest user reply: %s\nResolved subject context:\n%s\nActive conversation thread:\n%s\nDecision: %s\nTool result: %s\nProvide a concise answer.",
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
You are filling missing structured fields for an already chosen tool.

Return JSON array only. Return either:
- []
- or exactly one updated tool call for the same tool

Rules:
- Keep the same tool name: %s
- Preserve already known fields unless the latest user reply clearly corrects them.
- Preserve the same calculator operation when the pending tool is calculator.
- Fill only fields supported by the existing tool schema.
- If the pending calculator operation is bmi, bmr, or tdee and the latest reply is just a weight or height value, map it into the missing field instead of switching to convert.
- If the pending tool is memory_lookup and the latest reply clarifies which fact to look up, keep the same memory_lookup call.
- For pending bmi, bmr, or tdee fields, copy measurements exactly as the user stated them. Do not convert, normalize, or duplicate weight or height values.
- Use multi-component measurements only when the user explicitly gave a composite value like 5 ft 4 in.
- Use the active conversation thread to decide whether the latest reply is a clarification for the pending tool or a new unrelated request.
- Use the resolved subject context for references like "my brother", "Mark", or "his". Prefer explicit subject links over guesses.
- If the latest user reply does not supply the missing information and instead starts a new unrelated request, return [].
- Do not answer the user.
- Return valid JSON only.

Examples:
- Pending bmi with missing height + latest reply "64 inches" -> [{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]
- Pending bmi with missing weight + latest reply "45kg" -> [{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}],"weight":[{"unit":"kg","value":45}]}}]
- Pending memory_lookup asking "What fact should I look up?" + latest reply "weight" -> [{"tool":"memory_lookup","args":{"attribute":"weight"}}]

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
