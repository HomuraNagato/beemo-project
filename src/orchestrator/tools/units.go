package tools

import (
	"fmt"
	"regexp"
	"strings"
)

type unitDimensions struct {
	Length int
	Mass   int
	Time   int
	Volume int
	Amount int
}

type unitDefinition struct {
	Canonical string
	Factor    float64
	Dims      unitDimensions
	Category  measurementCategory
}

type parsedUnitExpression struct {
	Canonical string
	Factor    float64
	Dims      unitDimensions
}

var (
	unitExprTokenPattern = regexp.MustCompile(`(?i)[a-z]+(?:/[a-z]+)+`)
)

func (d unitDimensions) equal(other unitDimensions) bool {
	return d == other
}

func (d unitDimensions) reciprocalOf(other unitDimensions) bool {
	return d.Length == -other.Length &&
		d.Mass == -other.Mass &&
		d.Time == -other.Time &&
		d.Volume == -other.Volume &&
		d.Amount == -other.Amount
}

func (d unitDimensions) subtract(other unitDimensions) unitDimensions {
	return unitDimensions{
		Length: d.Length - other.Length,
		Mass:   d.Mass - other.Mass,
		Time:   d.Time - other.Time,
		Volume: d.Volume - other.Volume,
		Amount: d.Amount - other.Amount,
	}
}

func canonicalSimpleUnitToken(token string) (string, bool) {
	def, ok := simpleUnitDefinition(token)
	if !ok {
		return "", false
	}
	return def.Canonical, true
}

func simpleUnitDefinition(token string) (unitDefinition, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "mm", "millimeter", "millimeters", "millimetre", "millimetres":
		return unitDefinition{Canonical: "mm", Factor: 0.1, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "cm", "centimeter", "centimeters", "centimetre", "centimetres":
		return unitDefinition{Canonical: "cm", Factor: 1, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "m", "meter", "meters", "metre", "metres":
		return unitDefinition{Canonical: "m", Factor: 100, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "km", "kilometer", "kilometers", "kilometre", "kilometres":
		return unitDefinition{Canonical: "km", Factor: 100000, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "in", "inch", "inches":
		return unitDefinition{Canonical: "in", Factor: 2.54, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "ft", "foot", "feet":
		return unitDefinition{Canonical: "ft", Factor: 30.48, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "mi", "mile", "miles":
		return unitDefinition{Canonical: "mi", Factor: 160934.4, Dims: unitDimensions{Length: 1}, Category: measurementLength}, true
	case "mg", "milligram", "milligrams":
		return unitDefinition{Canonical: "mg", Factor: 0.000001, Dims: unitDimensions{Mass: 1}, Category: measurementWeight}, true
	case "g", "gram", "grams", "gr":
		return unitDefinition{Canonical: "g", Factor: 0.001, Dims: unitDimensions{Mass: 1}, Category: measurementWeight}, true
	case "kg", "kgs", "kilogram", "kilograms", "kiloggram", "kiloggrams":
		return unitDefinition{Canonical: "kg", Factor: 1, Dims: unitDimensions{Mass: 1}, Category: measurementWeight}, true
	case "lb", "lbs", "pound", "pounds":
		return unitDefinition{Canonical: "lb", Factor: 0.45359237, Dims: unitDimensions{Mass: 1}, Category: measurementWeight}, true
	case "ml", "milliliter", "milliliters", "millilitre", "millilitres":
		return unitDefinition{Canonical: "ml", Factor: 0.001, Dims: unitDimensions{Volume: 1}, Category: measurementVolume}, true
	case "l", "liter", "liters", "litre", "litres":
		return unitDefinition{Canonical: "l", Factor: 1, Dims: unitDimensions{Volume: 1}, Category: measurementVolume}, true
	case "s", "sec", "secs", "second", "seconds":
		return unitDefinition{Canonical: "s", Factor: 1, Dims: unitDimensions{Time: 1}, Category: measurementTime}, true
	case "min", "mins", "minute", "minutes":
		return unitDefinition{Canonical: "min", Factor: 60, Dims: unitDimensions{Time: 1}, Category: measurementTime}, true
	case "hr", "hrs", "h", "hour", "hours":
		return unitDefinition{Canonical: "hr", Factor: 3600, Dims: unitDimensions{Time: 1}, Category: measurementTime}, true
	case "mmol", "millimole", "millimoles":
		return unitDefinition{Canonical: "mmol", Factor: 0.001, Dims: unitDimensions{Amount: 1}, Category: measurementAmount}, true
	case "mol", "mole", "moles":
		return unitDefinition{Canonical: "mol", Factor: 1, Dims: unitDimensions{Amount: 1}, Category: measurementAmount}, true
	default:
		return unitDefinition{}, false
	}
}

func parseUnitExpression(text string) (parsedUnitExpression, error) {
	normalized := normalizeUnitExpressionText(text)
	if normalized == "" {
		return parsedUnitExpression{}, fmt.Errorf("unit expression is required")
	}

	parts := strings.Split(normalized, "/")
	if len(parts) == 0 {
		return parsedUnitExpression{}, fmt.Errorf("invalid unit expression: %s", text)
	}

	factor := 1.0
	dims := unitDimensions{}
	canonicalParts := make([]string, 0, len(parts))
	for i, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			return parsedUnitExpression{}, fmt.Errorf("invalid unit expression: %s", text)
		}
		def, ok := simpleUnitDefinition(part)
		if !ok {
			return parsedUnitExpression{}, fmt.Errorf("unsupported unit expression: %s", text)
		}
		canonicalParts = append(canonicalParts, def.Canonical)
		if i == 0 {
			factor *= def.Factor
			dims = unitDimensions{
				Length: dims.Length + def.Dims.Length,
				Mass:   dims.Mass + def.Dims.Mass,
				Time:   dims.Time + def.Dims.Time,
				Volume: dims.Volume + def.Dims.Volume,
				Amount: dims.Amount + def.Dims.Amount,
			}
			continue
		}
		factor /= def.Factor
		dims = unitDimensions{
			Length: dims.Length - def.Dims.Length,
			Mass:   dims.Mass - def.Dims.Mass,
			Time:   dims.Time - def.Dims.Time,
			Volume: dims.Volume - def.Dims.Volume,
			Amount: dims.Amount - def.Dims.Amount,
		}
	}

	return parsedUnitExpression{
		Canonical: strings.Join(canonicalParts, "/"),
		Factor:    factor,
		Dims:      dims,
	}, nil
}

func normalizeUnitExpressionText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " \t\n\r.,!?")
	normalized = strings.ReplaceAll(normalized, " ", "")
	switch normalized {
	case "mph":
		return "mi/hr"
	case "kph", "kmh":
		return "km/hr"
	case "mps":
		return "m/s"
	case "min_per_km":
		return "min/km"
	case "min_per_mile":
		return "min/mi"
	case "km_h":
		return "km/hr"
	case "m_s":
		return "m/s"
	}
	return normalized
}

func convertExpressionValue(value float64, fromExpr, toExpr string) (float64, string, string, error) {
	if fromTemp, ok := canonicalTemperatureUnit(fromExpr); ok {
		toTemp, ok := canonicalTemperatureUnit(toExpr)
		if !ok {
			return 0, "", "", fmt.Errorf("unsupported conversion: %s to %s", fromExpr, toExpr)
		}
		converted, err := convertUnits(value, fromTemp, toTemp)
		if err != nil {
			return 0, "", "", err
		}
		return converted, fromTemp, toTemp, nil
	}

	fromParsed, err := parseUnitExpression(fromExpr)
	if err != nil {
		return 0, "", "", err
	}
	baseValue := value * fromParsed.Factor
	converted, targetCanonical, err := convertBaseValueToExpression(baseValue, fromParsed.Dims, toExpr)
	if err != nil {
		return 0, "", "", err
	}
	return converted, fromParsed.Canonical, targetCanonical, nil
}

func convertBaseValueToExpression(baseValue float64, fromDims unitDimensions, toExpr string) (float64, string, error) {
	target, err := parseUnitExpression(toExpr)
	if err != nil {
		return 0, "", err
	}
	switch {
	case fromDims.equal(target.Dims):
		return baseValue / target.Factor, target.Canonical, nil
	case fromDims.reciprocalOf(target.Dims):
		if baseValue == 0 {
			return 0, "", fmt.Errorf("value must not be zero for reciprocal conversion")
		}
		return 1 / (baseValue * target.Factor), target.Canonical, nil
	default:
		return 0, "", fmt.Errorf("incompatible conversion: %s", target.Canonical)
	}
}

func dimensionsForCategory(category measurementCategory) (unitDimensions, bool) {
	switch category {
	case measurementLength:
		return unitDimensions{Length: 1}, true
	case measurementWeight:
		return unitDimensions{Mass: 1}, true
	case measurementTime:
		return unitDimensions{Time: 1}, true
	case measurementVolume:
		return unitDimensions{Volume: 1}, true
	case measurementAmount:
		return unitDimensions{Amount: 1}, true
	default:
		return unitDimensions{}, false
	}
}

func findCompoundUnitExpression(text string) (string, bool) {
	match := unitExprTokenPattern.FindString(text)
	if match == "" {
		return "", false
	}
	parsed, err := parseUnitExpression(match)
	if err != nil {
		return "", false
	}
	return parsed.Canonical, true
}

func canonicalTemperatureUnit(token string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "c", "celsius":
		return "c", true
	case "f", "fahrenheit":
		return "f", true
	case "k", "kelvin":
		return "k", true
	default:
		return "", false
	}
}
