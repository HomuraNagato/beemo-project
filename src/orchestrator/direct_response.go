package main

import (
	"fmt"
	"strings"
	"time"

	orchtools "eve-beemo/src/orchestrator/tools"
)

func directToolResponse(enabled bool, results []orchtools.Result, userQuery string) (string, bool) {
	if !enabled {
		return "", false
	}
	if len(results) != 1 {
		return "", false
	}
	result := results[0]
	output := strings.TrimSpace(result.Output)
	if output == "" || strings.TrimSpace(result.Action) != "get_time" || !isSimpleCurrentTimeQuery(userQuery) {
		return "", false
	}
	timestamp, err := time.Parse(time.RFC3339, output)
	if err != nil {
		return output, true
	}
	return fmt.Sprintf("It is %s.", timestamp.Format("3:04 PM on January 2, 2006")), true
}

func toolResultForPrompt(result orchtools.Result) string {
	output := strings.TrimSpace(result.Output)
	if strings.TrimSpace(result.Action) != "get_time" || output == "" {
		return output
	}
	timestamp, err := time.Parse(time.RFC3339, output)
	if err != nil {
		return output
	}
	return fmt.Sprintf("%s (%s)", timestamp.Format("January 2, 2006 at 3:04 PM MST"), output)
}

func isSimpleCurrentTimeQuery(text string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if lower == "" {
		return false
	}
	for _, term := range []string{
		"tomorrow", "yesterday", "next ", "last ", "after", "before", "from today",
		"in ", " ago", "later", "will ", "days", "weeks", "months", "years",
	} {
		if strings.Contains(lower, term) {
			return false
		}
	}
	return strings.Contains(lower, "what time") ||
		strings.Contains(lower, "what is the time") ||
		strings.Contains(lower, "what's the time") ||
		strings.Contains(lower, "whats the time") ||
		strings.Contains(lower, "tell me the time") ||
		strings.Contains(lower, "current time") ||
		strings.Contains(lower, "time is it") ||
		strings.Contains(lower, "right now") ||
		strings.Contains(lower, "today's date") ||
		strings.Contains(lower, "todays date") ||
		strings.Contains(lower, "current date") ||
		strings.Contains(lower, "what date") ||
		strings.Contains(lower, "what day") ||
		strings.Contains(lower, "what month") ||
		strings.Contains(lower, "what year") ||
		lower == "time" ||
		lower == "date"
}
