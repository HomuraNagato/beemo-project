package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ResolveCalculatorCall(call PlannedCall, explicitText string, snapshot map[string]json.RawMessage) (PlannedCall, error) {
	if strings.TrimSpace(call.Action) != "calculator" {
		return call, nil
	}

	args := map[string]json.RawMessage{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return PlannedCall{}, fmt.Errorf("invalid calculator args for resolution: %w", err)
		}
	}

	attrs := relevantAttrsForOperation(stringFieldRaw(args["operation"]))
	if len(attrs) == 0 {
		return call, nil
	}

	explicit := map[string]json.RawMessage{}
	if patch, ok, err := ExtractCalculatorObservationPatch(explicitText); err != nil {
		return PlannedCall{}, err
	} else if ok {
		if err := json.Unmarshal(patch, &explicit); err != nil {
			return PlannedCall{}, fmt.Errorf("invalid calculator observation patch for resolution: %w", err)
		}
	}

	changed := false
	for _, attr := range attrs {
		if raw, ok := explicit[attr]; ok && hasResolvedValue(attr, raw) {
			if string(args[attr]) != string(raw) {
				args[attr] = cloneRaw(raw)
				changed = true
			}
			continue
		}
		if raw, ok := snapshot[attr]; ok && hasResolvedValue(attr, raw) {
			if string(args[attr]) != string(raw) {
				args[attr] = cloneRaw(raw)
				changed = true
			}
		}
	}

	for _, attr := range attrs {
		raw, ok := args[attr]
		if !ok || !hasResolvedValue(attr, raw) {
			continue
		}
		canonical, err := canonicalizeResolvedAttr(attr, raw)
		if err != nil {
			return PlannedCall{}, err
		}
		if canonical != nil && string(canonical) != string(raw) {
			args[attr] = canonical
			changed = true
		}
	}

	if !changed {
		return call, nil
	}

	updated, err := json.Marshal(args)
	if err != nil {
		return PlannedCall{}, err
	}
	call.Args = updated
	return call, nil
}

func CanonicalizeObservationValue(attr string, raw json.RawMessage) (json.RawMessage, error) {
	if !hasResolvedValue(attr, raw) {
		return cloneRaw(raw), nil
	}
	return canonicalizeResolvedAttr(attr, raw)
}

func canonicalizeResolvedAttr(attr string, raw json.RawMessage) (json.RawMessage, error) {
	switch attr {
	case "weight":
		return canonicalizeMeasurementRaw(raw, measurementWeight, "kg", selectWeightComponents)
	case "height":
		return canonicalizeMeasurementRaw(raw, measurementLength, "cm", selectHeightComponents)
	default:
		return cloneRaw(raw), nil
	}
}

func canonicalizeMeasurementRaw(
	raw json.RawMessage,
	expected measurementCategory,
	targetUnit string,
	selectComponents func([]measurementComponent) []measurementComponent,
) (json.RawMessage, error) {
	var components []measurementComponent
	if err := json.Unmarshal(raw, &components); err != nil {
		return nil, fmt.Errorf("invalid measurement components: %w", err)
	}

	selected := selectComponents(filterMeasurementComponentsByCategory(components, expected))
	if len(selected) == 0 {
		return nil, nil
	}

	value, category, _, err := normalizeMeasurement(selected)
	if err != nil {
		return nil, err
	}
	if category != expected {
		return nil, fmt.Errorf("measurement components must use %s units", expected)
	}

	converted, err := convertNormalized(value, category, targetUnit)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal([]measurementComponent{{Unit: targetUnit, Value: converted}})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func selectWeightComponents(input []measurementComponent) []measurementComponent {
	if len(input) == 0 {
		return nil
	}
	return []measurementComponent{normalizeComponentUnit(input[0])}
}

func selectHeightComponents(input []measurementComponent) []measurementComponent {
	if len(input) == 0 {
		return nil
	}

	var feet *measurementComponent
	var inches *measurementComponent
	for _, component := range input {
		normalized := normalizeComponentUnit(component)
		switch normalized.Unit {
		case "ft":
			if feet == nil {
				copy := normalized
				feet = &copy
			}
		case "in":
			if feet != nil && inches == nil {
				copy := normalized
				inches = &copy
			}
		}
	}
	if feet != nil {
		selected := []measurementComponent{*feet}
		if inches != nil {
			selected = append(selected, *inches)
		}
		return selected
	}

	return []measurementComponent{normalizeComponentUnit(input[0])}
}

func normalizeComponentUnit(component measurementComponent) measurementComponent {
	unit := strings.ToLower(strings.TrimSpace(component.Unit))
	if def, ok := simpleUnitDefinition(unit); ok {
		unit = def.Canonical
	}
	return measurementComponent{
		Unit:  unit,
		Value: component.Value,
	}
}

func hasResolvedValue(attr string, raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	switch attr {
	case "weight", "height":
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return false
		}
		return len(items) > 0
	case "age_years":
		var value *float64
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return false
		}
		return true
	case "gender", "activity_level":
		return strings.TrimSpace(stringFieldRaw(raw)) != ""
	default:
		return false
	}
}

func relevantAttrsForOperation(operation string) []string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "bmi":
		return []string{"weight", "height"}
	case "bmr":
		return []string{"weight", "height", "age_years", "gender"}
	case "tdee":
		return []string{"weight", "height", "age_years", "gender", "activity_level"}
	default:
		return nil
	}
}

func stringFieldRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return json.RawMessage(cloned)
}
