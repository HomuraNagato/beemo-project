package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type PlannedCall struct {
	Action string
	Args   json.RawMessage
}

type PendingFillRequest struct {
	Action  string
	Args    json.RawMessage
	Missing []string
	Reply   string
}

var (
	weightEvidencePattern   = regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*(kg|kgs|kilograms?|g|grams?|lb|lbs|pounds?)\b`)
	heightEvidencePattern   = regexp.MustCompile(`(?i)(\b\d+(?:\.\d+)?\s*(ft|feet|foot|in|inch|inches|mm|millimeters?|millimetres?|cm|centimeters?|centimetres?|m|meters?|metres?)\b|\b\d+\s*'\s*\d*(?:\.\d+)?\s*"?|\b\d+(?:\.\d+)?\s*")`)
	ageEvidencePattern      = regexp.MustCompile(`(?i)(\b\d+(?:\.\d+)?\s*(years?\s*old|years?|yrs?|yr|yo|y/o)\b|\bage\s+\d+(?:\.\d+)?\b)`)
	genderEvidencePattern   = regexp.MustCompile(`(?i)\b(male|female|man|woman)\b`)
	activityEvidencePattern = regexp.MustCompile(`(?i)\b(sedentary|light|moderate|active|very active|very_active)\b`)

	resumeWeightPattern            = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(kg|kgs|kilogram|kilograms|kiloggram|kiloggrams|g|gram|grams|gr|lb|lbs|pound|pounds)\b`)
	resumeMeasurementPattern       = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(mm|millimeter|millimeters|millimetre|millimetres|cm|centimeter|centimeters|centimetre|centimetres|m|meter|meters|metre|metres|km|kilometer|kilometers|kilometre|kilometres|in|inch|inches|ft|foot|feet|mi|mile|miles|mg|milligram|milligrams|kg|kgs|kilogram|kilograms|kiloggram|kiloggrams|g|gram|grams|gr|lb|lbs|pound|pounds|ml|milliliter|milliliters|millilitre|millilitres|l|liter|liters|litre|litres|s|sec|secs|second|seconds|min|mins|minute|minutes|hr|hrs|h|hour|hours|mmol|millimole|millimoles|mol|mole|moles)\b`)
	resumeFeetInchesPattern        = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:ft|foot|feet|')\s*(?:(\d+(?:\.\d+)?)\s*(?:in|inch|inches|")?)?`)
	resumeInchesQuotePattern       = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*"`)
	resumeAgeExplicitPattern       = regexp.MustCompile(`(?i)(?:\b(\d{1,3}(?:\.\d+)?)\s*(years?\s*old|years?|yrs?|yr|yo|y/o)\b|\bage(?:\s+is)?\s+(\d{1,3}(?:\.\d+)?)\b)`)
	resumeBareNumberPattern        = regexp.MustCompile(`^\s*(\d{1,3})(?:\.0+)?\s*[\.,!?]*\s*$`)
	convertValueUnitPattern        = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*([a-z]+(?:/[a-z]+)+|mph|kph|kmh|mps|mm|millimeters?|millimetres?|cm|centimeters?|centimetres?|m|meters?|metres?|km|kilometers?|kilometres?|in|inch|inches|ft|foot|feet|mi|mile|miles|mg|milligrams?|g|grams?|gr|kg|kgs|kilograms?|kiloggrams?|lb|lbs|pounds?|ml|milliliters?|millilitres?|l|liters?|litres?|s|secs?|seconds?|min|mins?|minutes?|hr|hrs?|hours?|mmol|millimoles?|mol|moles?)\b`)
	convertTargetUnitPattern       = regexp.MustCompile(`(?i)\b(?:to|into|in)\s+([a-z]+(?:/[a-z]+)+|[a-z]+(?:\s+per\s+[a-z]+)?)\s*[\.\?!,]*$`)
	weatherHourPattern             = regexp.MustCompile(`(?i)\bat\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	birthdayPattern                = regexp.MustCompile(`(?i)\b(?:my\s+)?birthday\s+(?:is|was|on)\s+(.+?)\s*[\.\?!]*$`)
	startDatePattern               = regexp.MustCompile(`(?i)\b(?:i\s+)?started\s+(?:my\s+)?(.+?)\s+(?:on|at)\s+(.+?)\s*[\.\?!]*$`)
	favoriteColorPattern           = regexp.MustCompile(`(?i)\b(?:my\s+)?favou?rite\s+colou?r\s+(?:is|was)\s+(.+?)\s*[\.\?!]*$`)
	namedTextFactPattern           = regexp.MustCompile(`(?i)\bmy\s+([a-z][a-z0-9 _-]{1,40}?)\s+(?:is|was)\s+(.+?)\s*[\.\?!]*$`)
	genericMyFactPattern           = regexp.MustCompile(`(?i)\b(?:my|mine)\s+([a-z][a-z0-9 _-]{1,64}?)\s+(?:is|are|was|were|=)\s+(.+?)\s*[\.\?!]*$`)
	genericSetFactPattern          = regexp.MustCompile(`(?i)\b(?:set|update|change|correct|remember)\s+(?:that\s+)?(?:my|mine)\s+([a-z][a-z0-9 _-]{1,64}?)\s+(?:to|as|=)\s+(.+?)\s*[\.\?!]*$`)
	genericShouldFactPattern       = regexp.MustCompile(`(?i)\b(?:my|mine)\s+([a-z][a-z0-9 _-]{1,64}?)\s+(?:should\s+be|needs\s+to\s+be|changed\s+to)\s+(.+?)\s*[\.\?!]*$`)
	genericRecallAttrPattern       = regexp.MustCompile(`(?i)\b(?:what(?:'s| is| was)|who(?:'s| is| was)|when(?:'s| is| was)|where(?:'s| is| was)|tell me|remind me|recall|do you remember|remember|do you know)\s+(?:about\s+|of\s+)?(?:my|mine)\s+(.+?)\s*[\?\.\!]*$`)
	genericRecallSayAttrPattern    = regexp.MustCompile(`(?i)\bwhat\s+did\s+i\s+say\s+(?:my|mine)\s+(.+?)\s+(?:was|is)\s*[\?\.\!]*$`)
	genericRecallWhatAttrPattern   = regexp.MustCompile(`(?i)\b(?:tell me|remind me|recall|do you remember|remember|do you know)\s+(?:about\s+|of\s+)?(?:what|who|when|where)\s+(?:my|mine)\s+(.+?)\s+(?:is|was)\s*[\?\.\!]*$`)
	genericRecallMemoryAttrPattern = regexp.MustCompile(`(?i)\bwhat\s+do\s+you\s+(?:remember|know)\s+(?:about\s+|of\s+)(?:my|mine)\s+(.+?)\s*[\?\.\!]*$`)
	percentOfPattern               = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*(?:%|percent)\s+of\s+(\d+(?:\.\d+)?)\b`)
)

func TryFillPending(req PendingFillRequest) (PlannedCall, bool, error) {
	if req.Action == "weather" {
		reply := strings.TrimSpace(req.Reply)
		if reply == "" {
			return PlannedCall{}, false, nil
		}
		for _, field := range req.Missing {
			if field == "location" {
				raw, err := json.Marshal(map[string]string{"location": reply})
				if err != nil {
					return PlannedCall{}, false, err
				}
				merged, err := mergePendingArgs(req.Action, req.Args, req.Missing, raw)
				if err != nil {
					return PlannedCall{}, false, err
				}
				return PlannedCall{Action: req.Action, Args: merged}, true, nil
			}
		}
		return PlannedCall{}, false, nil
	}
	if req.Action != "calculator" {
		return PlannedCall{}, false, nil
	}

	update := map[string]any{}
	pendingOp := pendingOperation(req.Args)
	if pendingOp == "convert" {
		mergeConvertUpdate(update, req.Reply)
	}
	for _, field := range req.Missing {
		switch field {
		case "weight":
			if components, ok := parseWeightComponents(req.Reply); ok {
				update["weight"] = components
			}
		case "height":
			if components, ok := parseHeightComponents(req.Reply); ok {
				update["height"] = components
			}
		case "distance":
			if components, ok := parseGenericMeasurementComponents(req.Reply); ok {
				update["distance"] = components
			}
		case "input":
			if components, ok := parseGenericMeasurementComponents(req.Reply); ok {
				update["input"] = components
			}
		case "per":
			if components, ok := parseGenericMeasurementComponents(req.Reply); ok {
				update["per"] = components
			}
		case "age_years":
			if age, ok := parseAgeYears(req.Reply, len(req.Missing) == 1); ok {
				update["age_years"] = age
			}
		case "gender":
			if gender, ok := parseGender(req.Reply); ok {
				update["gender"] = gender
			}
		case "activity_level":
			if level, ok := parseActivityLevel(req.Reply); ok {
				update["activity_level"] = level
			}
		case "to_unit":
			if unit, ok := parseUnitOnly(req.Reply); ok {
				update["to_unit"] = unit
			}
		case "from_unit":
			if unit, ok := parseUnitOnly(req.Reply); ok {
				update["from_unit"] = unit
			}
		case "pace_unit":
			if unit, ok := parsePaceUnit(req.Reply); ok {
				update["pace_unit"] = unit
			}
		case "speed_unit":
			if unit, ok := parseSpeedUnit(req.Reply); ok {
				update["speed_unit"] = unit
			}
		case "direction":
			if direction, ok := parseDirection(req.Reply); ok {
				update["direction"] = direction
			}
		}
	}
	if len(update) == 0 {
		return PlannedCall{}, false, nil
	}

	raw, err := json.Marshal(update)
	if err != nil {
		return PlannedCall{}, false, err
	}
	merged, err := mergePendingArgs(req.Action, req.Args, req.Missing, raw)
	if err != nil {
		return PlannedCall{}, false, err
	}
	return PlannedCall{Action: req.Action, Args: merged}, true, nil
}

func MergePendingCall(pendingAction string, pendingArgs json.RawMessage, missing []string, resumed PlannedCall) (PlannedCall, bool, error) {
	if strings.TrimSpace(resumed.Action) != pendingAction {
		return PlannedCall{}, false, nil
	}
	mergedArgs, err := mergePendingArgs(pendingAction, pendingArgs, missing, resumed.Args)
	if err != nil {
		return PlannedCall{}, false, err
	}
	return PlannedCall{Action: pendingAction, Args: mergedArgs}, true, nil
}

func GroundCall(evidenceText string, call PlannedCall) (PlannedCall, error) {
	if call.Action != "calculator" {
		return call, nil
	}

	args := map[string]any{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return PlannedCall{}, fmt.Errorf("invalid calculator args for grounding: %w", err)
		}
	}

	switch strings.ToLower(strings.TrimSpace(stringField(args["operation"]))) {
	case "bmi":
		if !weightEvidencePattern.MatchString(evidenceText) {
			delete(args, "weight")
		}
		if !heightEvidencePattern.MatchString(evidenceText) {
			delete(args, "height")
		}
	case "bmr":
		if !weightEvidencePattern.MatchString(evidenceText) {
			delete(args, "weight")
		}
		if !heightEvidencePattern.MatchString(evidenceText) {
			delete(args, "height")
		}
		if !ageEvidencePattern.MatchString(evidenceText) {
			delete(args, "age_years")
		}
		if !genderEvidencePattern.MatchString(evidenceText) {
			delete(args, "gender")
		}
	case "tdee":
		if !weightEvidencePattern.MatchString(evidenceText) {
			delete(args, "weight")
		}
		if !heightEvidencePattern.MatchString(evidenceText) {
			delete(args, "height")
		}
		if !ageEvidencePattern.MatchString(evidenceText) {
			delete(args, "age_years")
		}
		if !genderEvidencePattern.MatchString(evidenceText) {
			delete(args, "gender")
		}
		if !activityEvidencePattern.MatchString(evidenceText) {
			delete(args, "activity_level")
		}
	}

	groundedArgs, err := json.Marshal(args)
	if err != nil {
		return PlannedCall{}, err
	}
	call.Args = groundedArgs
	return call, nil
}

func ExtractCalculatorObservationPatch(text string) (json.RawMessage, bool, error) {
	return ExtractObservationPatch(text)
}

func ExtractObservationPatch(text string) (json.RawMessage, bool, error) {
	update := map[string]any{}
	if components, ok := parseWeightComponents(text); ok {
		update["weight"] = components
	}
	if components, ok := parseHeightComponents(text); ok {
		update["height"] = components
	}
	if age, ok := parseAgeYears(text, false); ok {
		update["age_years"] = age
	}
	if gender, ok := parseGender(text); ok {
		update["gender"] = gender
	}
	if level, ok := parseActivityLevel(text); ok {
		update["activity_level"] = level
	}
	for key, value := range extractTextObservations(text) {
		if _, exists := update[key]; !exists {
			update[key] = value
		}
	}
	if len(update) == 0 {
		return nil, false, nil
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func InferToolCall(text string) (PlannedCall, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return PlannedCall{}, false, nil
	}
	lower := strings.ToLower(trimmed)

	switch {
	case isOlderSisterRequest(lower):
		return rawPlannedCall("older_sister", map[string]any{
			"query":      olderSisterQuery(trimmed),
			"web_search": true,
		})
	case isMemoryRecallRequest(lower):
		args := map[string]any{}
		if attr := inferredMemoryAttr(lower); attr != "" {
			args["attribute"] = attr
		}
		return rawPlannedCall("memory_lookup", args)
	case isWeatherRequest(lower):
		return rawPlannedCall("weather", extractWeatherUpdate(trimmed))
	case isTimeRequest(lower):
		return rawPlannedCall("get_time", map[string]any{})
	case strings.Contains(lower, "bmi"):
		return rawPlannedCall("calculator", calculatorArgsFromText(trimmed, "bmi"))
	case strings.Contains(lower, "bmr"):
		return rawPlannedCall("calculator", calculatorArgsFromText(trimmed, "bmr"))
	case strings.Contains(lower, "tdee"):
		return rawPlannedCall("calculator", calculatorArgsFromText(trimmed, "tdee"))
	case strings.Contains(lower, "convert "):
		return rawPlannedCall("calculator", calculatorArgsFromText(trimmed, "convert"))
	}
	if matches := percentOfPattern.FindStringSubmatch(lower); len(matches) == 3 {
		percent, perr := strconv.ParseFloat(matches[1], 64)
		value, verr := strconv.ParseFloat(matches[2], 64)
		if perr == nil && verr == nil {
			return rawPlannedCall("calculator", map[string]any{
				"operation": "percent_of",
				"percent":   percent,
				"value":     value,
			})
		}
	}
	return PlannedCall{}, false, nil
}

func rawPlannedCall(action string, args map[string]any) (PlannedCall, bool, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return PlannedCall{}, false, err
	}
	return PlannedCall{Action: action, Args: raw}, true, nil
}

func calculatorArgsFromText(text, operation string) map[string]any {
	args := map[string]any{"operation": operation}
	if components, ok := parseWeightComponents(text); ok {
		args["weight"] = components
	}
	if components, ok := parseHeightComponents(text); ok {
		args["height"] = components
	}
	if age, ok := parseAgeYears(text, false); ok {
		args["age_years"] = age
	}
	if gender, ok := parseGender(text); ok {
		args["gender"] = gender
	}
	if level, ok := parseActivityLevel(text); ok {
		args["activity_level"] = level
	}
	if operation == "convert" {
		mergeConvertUpdate(args, text)
	}
	return args
}

func isTimeRequest(lower string) bool {
	if !containsAny(lower, "time", "date", "day", "month", "year", "today", "tomorrow", "yesterday") {
		return false
	}
	return containsAny(lower, "what", "when", "current", "right now", "now", "today", "tomorrow", "yesterday")
}

func isWeatherRequest(lower string) bool {
	return containsAny(lower, "weather", "temperature", "forecast", "rain", "raining", "snow")
}

func isOlderSisterRequest(lower string) bool {
	return containsAny(lower, "older sister", "chatgpt", "search the internet", "look up", "lookup", "web search", "search for", "verify whether")
}

func isMemoryRecallRequest(lower string) bool {
	if containsAny(lower, "bmi", "bmr", "tdee") {
		return false
	}
	if inferredMemoryAttr(lower) != "" {
		return containsAny(lower,
			"what", "when", "how", "tell me", "remind me", "recall",
			"remember", "remembered", "did i say", "do you know",
		)
	}
	return containsAny(lower, "remember", "remembered", "did i say", "do you know")
}

func inferredMemoryAttr(lower string) string {
	switch {
	case containsAny(lower, "weight", "weigh"):
		return "weight"
	case containsAny(lower, "height", "tall"):
		return "height"
	case containsAny(lower, "age", "how old"):
		return "age_years"
	case containsAny(lower, "gender"):
		return "gender"
	case containsAny(lower, "activity level", "how active"):
		return "activity_level"
	case containsAny(lower, "birthday", "birth day"):
		return "birthday"
	case containsAny(lower, "start date", "started", "new job"):
		return "start_date"
	case containsAny(lower, "favorite color", "favourite color"):
		return "favorite_color"
	case containsStandaloneWord(lower, "name"):
		return "name"
	default:
		return inferredGenericMemoryAttr(lower)
	}
}

func inferredGenericMemoryAttr(lower string) string {
	for _, candidate := range genericRecallSayAttrPattern.FindAllStringSubmatch(lower, -1) {
		if len(candidate) < 2 {
			continue
		}
		attr := normalizeMemoryLookupLabel(candidate[1])
		if isGenericFactLabel(attr) && !isCalculatedMemoryLabel(attr) {
			return attr
		}
	}
	for _, candidate := range genericRecallWhatAttrPattern.FindAllStringSubmatch(lower, -1) {
		if len(candidate) < 2 {
			continue
		}
		attr := normalizeMemoryLookupLabel(candidate[1])
		if isGenericFactLabel(attr) && !isCalculatedMemoryLabel(attr) {
			return attr
		}
	}
	for _, candidate := range genericRecallMemoryAttrPattern.FindAllStringSubmatch(lower, -1) {
		if len(candidate) < 2 {
			continue
		}
		attr := normalizeMemoryLookupLabel(candidate[1])
		if isGenericFactLabel(attr) && !isCalculatedMemoryLabel(attr) {
			return attr
		}
	}
	for _, candidate := range genericRecallAttrPattern.FindAllStringSubmatch(lower, -1) {
		if len(candidate) < 2 {
			continue
		}
		attr := normalizeMemoryLookupLabel(candidate[1])
		if isGenericFactLabel(attr) && !isCalculatedMemoryLabel(attr) {
			return attr
		}
	}
	return ""
}

func olderSisterQuery(text string) string {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{
		"ask older sister to ",
		"ask older sister ",
		"ask chatgpt to ",
		"ask chatgpt ",
		"search the internet for ",
		"search for ",
		"look up ",
	} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

func extractTextObservations(text string) map[string]any {
	out := map[string]any{}
	trimmed := strings.TrimSpace(text)
	if matches := birthdayPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		out["birthday"] = strings.TrimSpace(matches[1])
	}
	if matches := startDatePattern.FindStringSubmatch(trimmed); len(matches) == 3 {
		label := normalizeFactName(matches[1])
		value := strings.TrimSpace(matches[2])
		if label == "new_job" || strings.Contains(label, "job") || strings.Contains(label, "work") {
			out["start_date"] = value
		} else {
			out[label+"_start_date"] = value
		}
	}
	if matches := favoriteColorPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		out["favorite_color"] = strings.TrimSpace(matches[1])
	}
	if matches := namedTextFactPattern.FindStringSubmatch(trimmed); len(matches) == 3 {
		attr := normalizeFactName(matches[1])
		value := strings.TrimSpace(matches[2])
		switch attr {
		case "birthday", "favorite_color", "favourite_color", "name":
			if attr == "favourite_color" {
				attr = "favorite_color"
			}
			out[attr] = value
		}
	}
	for _, clause := range genericFactClauses(trimmed) {
		if matches := genericMyFactPattern.FindStringSubmatch(clause); len(matches) == 3 {
			attr := normalizeFactName(matches[1])
			value := cleanGenericFactValue(matches[2])
			if attr == "favourite_color" {
				attr = "favorite_color"
			}
			if isGenericFactLabel(attr) && value != "" {
				if _, exists := out[attr]; !exists {
					out[attr] = value
				}
			}
		}
		if matches := genericSetFactPattern.FindStringSubmatch(clause); len(matches) == 3 {
			attr := normalizeFactName(matches[1])
			value := cleanGenericFactValue(matches[2])
			if isGenericFactLabel(attr) && value != "" {
				if _, exists := out[attr]; !exists {
					out[attr] = value
				}
			}
		}
		if matches := genericShouldFactPattern.FindStringSubmatch(clause); len(matches) == 3 {
			attr := normalizeFactName(matches[1])
			value := cleanGenericFactValue(matches[2])
			if isGenericFactLabel(attr) && value != "" {
				if _, exists := out[attr]; !exists {
					out[attr] = value
				}
			}
		}
	}
	return out
}

func normalizeFactName(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, "-", " ")
	fields := strings.Fields(text)
	return strings.Join(fields, "_")
}

func normalizeMemoryLookupLabel(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.TrimSuffix(text, "?")
	text = strings.TrimSuffix(text, ".")
	text = strings.TrimSuffix(text, "!")
	for _, suffix := range []string{" again", " right now", " currently", " now"} {
		text = strings.TrimSuffix(text, suffix)
	}
	return normalizeFactName(text)
}

func genericFactClauses(text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"remember that ", "please remember that ", "please remember "} {
		if strings.HasPrefix(lower, prefix) {
			trimmed = strings.TrimSpace(trimmed[len(prefix):])
			break
		}
	}
	replacer := strings.NewReplacer(
		" and my ", "\nmy ",
		" and mine ", "\nmine ",
		"; my ", "\nmy ",
		"; mine ", "\nmine ",
		". my ", "\nmy ",
		". mine ", "\nmine ",
		", my ", "\nmy ",
		", mine ", "\nmine ",
	)
	split := strings.Split(replacer.Replace(trimmed), "\n")
	clauses := make([]string, 0, len(split))
	for _, clause := range split {
		clause = strings.TrimSpace(clause)
		if clause != "" {
			clauses = append(clauses, clause)
		}
	}
	return clauses
}

func cleanGenericFactValue(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, " \t\r\n\"'")
	for _, suffix := range []string{" from now on", " going forward"} {
		if strings.HasSuffix(strings.ToLower(text), suffix) {
			text = strings.TrimSpace(text[:len(text)-len(suffix)])
		}
	}
	return strings.TrimSpace(text)
}

func isGenericFactLabel(attr string) bool {
	attr = strings.Trim(attr, "_")
	if attr == "" || len(attr) > 80 {
		return false
	}
	if strings.Contains(attr, "__") {
		return false
	}
	if isRelationshipFactLabel(attr) {
		return false
	}
	for _, blocked := range []string{
		"bmi", "bmr", "tdee", "body_mass_index", "basal_metabolic_rate", "total_daily_energy_expenditure",
		"question", "request", "answer", "calculation", "conversion",
	} {
		if attr == blocked {
			return false
		}
	}
	return true
}

func isRelationshipFactLabel(attr string) bool {
	if strings.HasPrefix(attr, "friend") && len(attr) > len("friend") {
		suffix := strings.TrimPrefix(attr, "friend")
		for _, r := range suffix {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	switch attr {
	case "brother", "dad", "daughter", "father", "friend", "girlfriend", "boyfriend", "husband", "mom", "mother", "partner", "sister", "son", "trainer", "wife":
		return true
	default:
		return false
	}
}

func isCalculatedMemoryLabel(attr string) bool {
	for _, marker := range []string{"bmi", "bmr", "tdee", "body_mass_index", "metabolic_rate", "daily_energy"} {
		if strings.Contains(attr, marker) {
			return true
		}
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsStandaloneWord(text, word string) bool {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return false
	}
	for _, candidate := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if candidate == word {
			return true
		}
	}
	return false
}

func mergePendingArgs(action string, pendingArgs json.RawMessage, missing []string, updateRaw json.RawMessage) (json.RawMessage, error) {
	if action != "calculator" {
		base := map[string]any{}
		if len(pendingArgs) > 0 {
			if err := json.Unmarshal(pendingArgs, &base); err != nil {
				return nil, fmt.Errorf("invalid pending args: %w", err)
			}
		}
		if len(updateRaw) == 0 {
			if len(base) == 0 {
				return json.RawMessage(`{}`), nil
			}
			merged, err := json.Marshal(base)
			if err != nil {
				return nil, err
			}
			return merged, nil
		}
		update := map[string]any{}
		if err := json.Unmarshal(updateRaw, &update); err != nil {
			return nil, fmt.Errorf("invalid resume args: %w", err)
		}
		for key, value := range update {
			if value != nil {
				base[key] = value
			}
		}
		merged, err := json.Marshal(base)
		if err != nil {
			return nil, err
		}
		return merged, nil
	}

	base := map[string]any{}
	if len(pendingArgs) > 0 {
		if err := json.Unmarshal(pendingArgs, &base); err != nil {
			return nil, fmt.Errorf("invalid pending args: %w", err)
		}
	}

	update := map[string]any{}
	if len(updateRaw) > 0 {
		if err := json.Unmarshal(updateRaw, &update); err != nil {
			return nil, fmt.Errorf("invalid resume args: %w", err)
		}
	}

	pendingOp := strings.TrimSpace(stringField(base["operation"]))
	updateOp := strings.TrimSpace(stringField(update["operation"]))
	if pendingOp != "" && updateOp != "" && updateOp != pendingOp {
		coerced, ok := coerceResumeArgsForPending(pendingOp, missing, update)
		if !ok {
			return nil, fmt.Errorf("resume operation mismatch: pending=%s update=%s", pendingOp, updateOp)
		}
		update = coerced
	}

	allowed := make(map[string]struct{}, len(missing)+1)
	allowed["operation"] = struct{}{}
	for _, field := range missing {
		allowed[field] = struct{}{}
	}
	if pendingOp == "convert" {
		allowed["input"] = struct{}{}
		allowed["value"] = struct{}{}
		allowed["from_unit"] = struct{}{}
		allowed["to_unit"] = struct{}{}
		allowed["per"] = struct{}{}
	}

	for key, value := range update {
		if _, ok := allowed[key]; !ok {
			continue
		}
		if value != nil {
			base[key] = value
		}
	}
	if pendingOp != "" {
		base["operation"] = pendingOp
	}

	merged, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func coerceResumeArgsForPending(pendingOp string, missing []string, update map[string]any) (map[string]any, bool) {
	if pendingOp == "" || len(missing) != 1 {
		return nil, false
	}
	input, ok := update["input"]
	if !ok {
		return nil, false
	}
	switch missing[0] {
	case "weight", "height", "distance", "input", "per":
		return map[string]any{
			"operation": pendingOp,
			missing[0]:  input,
		}, true
	default:
		return nil, false
	}
}

func stringField(v any) string {
	s, _ := v.(string)
	return s
}

func pendingOperation(raw json.RawMessage) string {
	args := map[string]any{}
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(stringField(args["operation"])))
}

func extractWeatherUpdate(text string) map[string]any {
	update := map[string]any{}
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(lower, "tomorrow"):
		update["when"] = "tomorrow"
	case strings.Contains(lower, "this evening"), strings.Contains(lower, "tonight"), strings.Contains(lower, "evening"):
		update["when"] = "evening"
	case strings.Contains(lower, "today"):
		update["when"] = "today"
	case strings.Contains(lower, "temperature"), strings.Contains(lower, "temp"):
		update["when"] = "current"
	case strings.Contains(lower, "weather"), strings.Contains(lower, "forecast"):
		update["when"] = "current"
	}

	switch {
	case strings.Contains(lower, "rain"), strings.Contains(lower, "precip"):
		update["focus"] = "rain"
	case strings.Contains(lower, "temperature"), strings.Contains(lower, "temp"):
		update["focus"] = "temperature"
	case strings.Contains(lower, "weather"), strings.Contains(lower, "forecast"):
		update["focus"] = "general"
	}
	if hour, ok := parseWeatherHour(text); ok {
		update["hour_local"] = hour
		if _, ok := update["when"]; !ok {
			update["when"] = "today"
		}
	}
	if location := ExtractWeatherLocation(text); location != "" {
		update["location"] = location
	}
	return update
}

func parseWeatherHour(text string) (int, bool) {
	matches := weatherHourPattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	if len(matches) >= 3 && strings.TrimSpace(matches[2]) != "" && matches[2] != "00" {
		return 0, false
	}
	suffix := strings.ToLower(strings.TrimSpace(matches[3]))
	switch suffix {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour != 12 {
			hour += 12
		}
	default:
		if hour < 0 || hour > 23 {
			return 0, false
		}
	}
	if hour < 0 || hour > 23 {
		return 0, false
	}
	return hour, true
}

func parseWeightComponents(text string) ([]measurementComponent, bool) {
	matches := resumeWeightPattern.FindStringSubmatch(strings.ToLower(text))
	if len(matches) < 3 {
		return nil, false
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return nil, false
	}
	unit, ok := canonicalWeightUnit(matches[2])
	if !ok {
		return nil, false
	}
	return []measurementComponent{{Unit: unit, Value: value}}, true
}

func parseHeightComponents(text string) ([]measurementComponent, bool) {
	lower := strings.ToLower(text)
	if matches := resumeFeetInchesPattern.FindStringSubmatch(lower); len(matches) >= 2 {
		ft, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			components := []measurementComponent{{Unit: "ft", Value: ft}}
			if len(matches) >= 3 && strings.TrimSpace(matches[2]) != "" {
				inch, ierr := strconv.ParseFloat(matches[2], 64)
				if ierr == nil {
					components = append(components, measurementComponent{Unit: "in", Value: inch})
				}
			}
			return components, true
		}
	}
	if matches := resumeInchesQuotePattern.FindStringSubmatch(lower); len(matches) >= 2 {
		value, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return []measurementComponent{{Unit: "in", Value: value}}, true
		}
	}
	for _, matches := range resumeMeasurementPattern.FindAllStringSubmatch(lower, -1) {
		if len(matches) < 3 {
			continue
		}
		value, err := strconv.ParseFloat(matches[1], 64)
		if err != nil {
			continue
		}
		unit, ok := canonicalLengthUnit(matches[2])
		if !ok {
			continue
		}
		return []measurementComponent{{Unit: unit, Value: value}}, true
	}
	return nil, false
}

func parseGenericMeasurementComponents(text string) ([]measurementComponent, bool) {
	if components, ok := parseHeightComponents(text); ok {
		return components, true
	}
	if components, ok := parseWeightComponents(text); ok {
		return components, true
	}
	lower := strings.ToLower(text)
	if matches := resumeMeasurementPattern.FindStringSubmatch(lower); len(matches) >= 3 {
		value, err := strconv.ParseFloat(matches[1], 64)
		if err != nil {
			return nil, false
		}
		unit, ok := canonicalSimpleUnitToken(matches[2])
		if !ok {
			return nil, false
		}
		return []measurementComponent{{Unit: unit, Value: value}}, true
	}
	return nil, false
}

func extractConvertUpdate(text string) map[string]any {
	update := map[string]any{}
	if components, ok := parseGenericMeasurementComponents(text); ok {
		update["input"] = components
	} else if value, fromUnit, ok := parseValueWithUnitExpression(text); ok {
		update["value"] = value
		update["from_unit"] = fromUnit
	}
	if unit, ok := parseConvertTargetUnit(text); ok {
		update["to_unit"] = unit
	}
	if len(update) == 0 {
		return nil
	}
	return update
}

func mergeConvertUpdate(dst map[string]any, text string) {
	for key, value := range extractConvertUpdate(text) {
		dst[key] = value
	}
}

func parseValueWithUnitExpression(text string) (float64, string, bool) {
	matches := convertValueUnitPattern.FindStringSubmatch(strings.ToLower(text))
	if len(matches) < 3 {
		return 0, "", false
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, "", false
	}
	unit, ok := parseUnitOnly(matches[2])
	if !ok {
		return 0, "", false
	}
	return value, unit, true
}

func parseConvertTargetUnit(text string) (string, bool) {
	if matches := convertTargetUnitPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text))); len(matches) >= 2 {
		candidate := strings.ReplaceAll(matches[1], " per ", "/")
		if unit, ok := parseUnitOnly(candidate); ok {
			return unit, true
		}
	}
	return "", false
}

func parseAgeYears(text string, allowBareNumber bool) (float64, bool) {
	lower := strings.ToLower(text)
	if matches := resumeAgeExplicitPattern.FindStringSubmatch(lower); len(matches) >= 2 {
		for _, candidate := range []string{matches[1], matches[3]} {
			if strings.TrimSpace(candidate) == "" {
				continue
			}
			value, err := strconv.ParseFloat(candidate, 64)
			if err == nil {
				return value, true
			}
		}
	}
	if allowBareNumber {
		if matches := resumeBareNumberPattern.FindStringSubmatch(lower); len(matches) >= 2 {
			value, err := strconv.ParseFloat(matches[1], 64)
			if err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

func parseGender(text string) (string, bool) {
	lower := strings.ToLower(text)
	matches := genderEvidencePattern.FindStringSubmatch(lower)
	if len(matches) < 2 {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(matches[1])) {
	case "female", "woman":
		return "female", true
	case "male", "man":
		return "male", true
	default:
		return "", false
	}
}

func parseActivityLevel(text string) (string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "very active"), strings.Contains(lower, "very_active"):
		return "very_active", true
	case strings.Contains(lower, "moderate"):
		return "moderate", true
	case strings.Contains(lower, "sedentary"):
		return "sedentary", true
	case strings.Contains(lower, "light"):
		return "light", true
	case strings.Contains(lower, "active"):
		return "active", true
	default:
		return "", false
	}
}

func parseUnitOnly(text string) (string, bool) {
	if parsed, err := parseUnitExpression(text); err == nil {
		return parsed.Canonical, true
	}
	if expr, ok := findCompoundUnitExpression(text); ok {
		return expr, true
	}
	if matches := resumeMeasurementPattern.FindStringSubmatch(strings.ToLower(text)); len(matches) >= 3 {
		if unit, ok := canonicalSimpleUnitToken(matches[2]); ok {
			return unit, true
		}
	}
	for _, token := range strings.Fields(strings.ToLower(text)) {
		if unit, ok := canonicalTemperatureUnit(token); ok {
			return unit, true
		}
		if parsed, err := parseUnitExpression(token); err == nil {
			return parsed.Canonical, true
		}
		if unit, ok := canonicalSimpleUnitToken(token); ok {
			return unit, true
		}
	}
	return "", false
}

func parsePaceUnit(text string) (string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "min/km"), strings.Contains(lower, "minutes/km"), strings.Contains(lower, "minutes per km"), strings.Contains(lower, "minutes per kilometer"), strings.Contains(lower, "min per km"):
		return "min_per_km", true
	case strings.Contains(lower, "min/mile"), strings.Contains(lower, "minutes/mile"), strings.Contains(lower, "minutes per mile"), strings.Contains(lower, "min per mile"):
		return "min_per_mile", true
	default:
		return "", false
	}
}

func parseSpeedUnit(text string) (string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "km/h"), strings.Contains(lower, "kph"), strings.Contains(lower, "kmh"):
		return "km_h", true
	case strings.Contains(lower, "mph"):
		return "mph", true
	case strings.Contains(lower, "m/s"), strings.Contains(lower, "meters per second"), strings.Contains(lower, "metres per second"):
		return "m_s", true
	default:
		return "", false
	}
}

func parseDirection(text string) (string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "increase"), strings.Contains(lower, "up"):
		return "increase", true
	case strings.Contains(lower, "decrease"), strings.Contains(lower, "down"):
		return "decrease", true
	default:
		return "", false
	}
}

func canonicalWeightUnit(unit string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "kg", "kgs", "kilogram", "kilograms", "kiloggram", "kiloggrams":
		return "kg", true
	case "g", "gram", "grams", "gr":
		return "g", true
	case "lb", "lbs", "pound", "pounds":
		return "lb", true
	default:
		return "", false
	}
}

func canonicalLengthUnit(unit string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "cm", "centimeter", "centimeters", "centimetre", "centimetres":
		return "cm", true
	case "m", "meter", "meters", "metre", "metres":
		return "m", true
	case "km", "kilometer", "kilometers", "kilometre", "kilometres":
		return "km", true
	case "in", "inch", "inches":
		return "in", true
	case "ft", "foot", "feet":
		return "ft", true
	case "mi", "mile", "miles":
		return "mi", true
	default:
		return "", false
	}
}
