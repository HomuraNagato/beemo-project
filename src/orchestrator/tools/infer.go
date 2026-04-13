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
	heightEvidencePattern   = regexp.MustCompile(`(?i)(\b\d+(?:\.\d+)?\s*(ft|feet|foot|in|inch|inches|cm|m|meters?|metres?)\b|\b\d+\s*'\s*\d*(?:\.\d+)?\s*"?|\b\d+(?:\.\d+)?\s*")`)
	ageEvidencePattern      = regexp.MustCompile(`(?i)(\b\d+(?:\.\d+)?\s*(years?\s*old|years?|yrs?|yr|yo|y/o)\b|\bage\s+\d+(?:\.\d+)?\b)`)
	genderEvidencePattern   = regexp.MustCompile(`(?i)\b(male|female|man|woman)\b`)
	activityEvidencePattern = regexp.MustCompile(`(?i)\b(sedentary|light|moderate|active|very active|very_active)\b`)

	resumeWeightPattern      = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(kg|kgs|kilogram|kilograms|kiloggram|kiloggrams|g|gram|grams|gr|lb|lbs|pound|pounds)\b`)
	resumeMeasurementPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(mm|millimeter|millimeters|millimetre|millimetres|cm|centimeter|centimeters|centimetre|centimetres|m|meter|meters|metre|metres|km|kilometer|kilometers|kilometre|kilometres|in|inch|inches|ft|foot|feet|mi|mile|miles|mg|milligram|milligrams|kg|kgs|kilogram|kilograms|kiloggram|kiloggrams|g|gram|grams|gr|lb|lbs|pound|pounds|ml|milliliter|milliliters|millilitre|millilitres|l|liter|liters|litre|litres|s|sec|secs|second|seconds|min|mins|minute|minutes|hr|hrs|h|hour|hours|mmol|millimole|millimoles|mol|mole|moles)\b`)
	resumeFeetInchesPattern  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:ft|foot|feet|')\s*(?:(\d+(?:\.\d+)?)\s*(?:in|inch|inches|")?)?`)
	resumeInchesQuotePattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*"`)
	resumeAgeExplicitPattern = regexp.MustCompile(`(?i)\b(\d{1,3}(?:\.\d+)?)\s*(years?\s*old|years?|yrs?|yr|yo|y/o)\b`)
	resumeBareNumberPattern  = regexp.MustCompile(`^\s*(\d{1,3})(?:\.0+)?\s*[\.,!?]*\s*$`)
)

func TryFillPending(req PendingFillRequest) (PlannedCall, bool, error) {
	if req.Action != "calculator" {
		return PlannedCall{}, false, nil
	}

	update := map[string]any{}
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

func mergePendingArgs(action string, pendingArgs json.RawMessage, missing []string, updateRaw json.RawMessage) (json.RawMessage, error) {
	if action != "calculator" {
		if len(updateRaw) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return updateRaw, nil
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

func parseAgeYears(text string, allowBareNumber bool) (float64, bool) {
	lower := strings.ToLower(text)
	if matches := resumeAgeExplicitPattern.FindStringSubmatch(lower); len(matches) >= 2 {
		value, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return value, true
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
	switch {
	case strings.Contains(lower, "female"), strings.Contains(lower, "woman"):
		return "female", true
	case strings.Contains(lower, "male"), strings.Contains(lower, "man"):
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
