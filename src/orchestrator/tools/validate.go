package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ambiguousBareHeightPattern = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(\d{1,2})\s*(ft|feet|foot|in|inch|inches)\b`)
	adjacentWeightUnitsPattern = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*(kg|kgs|kilograms?|g|grams?|lb|lbs|pounds?)\s+(kg|kgs|kilograms?|g|grams?|lb|lbs|pounds?)\b`)
)

func ValidateCalculatorCall(call PlannedCall, explicitText string) (Result, bool, error) {
	if strings.TrimSpace(call.Action) != "calculator" {
		return Result{}, false, nil
	}

	args, err := parseCalculatorArgs(call.Args)
	if err != nil {
		return Result{}, false, err
	}

	if question, ok := ambiguousConversionQuestion(args, explicitText); ok {
		return needsInputResult(call.Action, []string{"input"}, question), true, nil
	}
	return Result{}, false, nil
}

func ambiguousConversionQuestion(args calculatorArgs, explicitText string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(explicitText))
	if lower == "" {
		return "", false
	}
	if strings.ToLower(strings.TrimSpace(args.Operation)) == "convert" && len(args.Input) > 2 {
		return "I heard multiple measurements. Which exact value should I convert, and to what unit?", true
	}
	if matches := ambiguousBareHeightPattern.FindStringSubmatch(lower); len(matches) >= 4 {
		return bareHeightQuestion(matches)
	}
	if matches := adjacentWeightUnitsPattern.FindStringSubmatch(lower); len(matches) >= 4 {
		return adjacentWeightUnitsQuestion(matches)
	}
	return "", false
}

func bareHeightQuestion(matches []string) (string, bool) {
	first := strings.TrimSpace(matches[1])
	second := strings.TrimSpace(matches[2])
	firstValue, firstErr := strconv.ParseFloat(first, 64)
	secondValue, secondErr := strconv.ParseFloat(second, 64)
	if firstErr != nil || secondErr != nil || firstValue < 3 || firstValue > 8 || secondValue < 0 || secondValue >= 12 {
		return "", false
	}

	unit := strings.ToLower(strings.TrimSpace(matches[3]))
	switch unit {
	case "ft", "feet", "foot":
		return fmt.Sprintf("Did you mean %s feet %s inches?", first, second), true
	case "in", "inch", "inches":
		return fmt.Sprintf("Did you mean %s feet %s inches, or %s inches plus %s inches?", first, second, first, second), true
	default:
		return "", false
	}
}

func adjacentWeightUnitsQuestion(matches []string) (string, bool) {
	value := strings.TrimSpace(matches[1])
	fromUnit, fromOK := canonicalSimpleUnitToken(matches[2])
	toUnit, toOK := canonicalSimpleUnitToken(matches[3])
	if fromOK && toOK && fromUnit != toUnit {
		return fmt.Sprintf("Should I convert %s %s to %s?", value, fromUnit, toUnit), true
	}
	return "", false
}
