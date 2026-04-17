package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Request struct {
	SessionID string
	Action    string
	Args      json.RawMessage
}

type Result struct {
	Action   string
	Output   string
	Status   string
	Missing  []string
	Question string
}

type Executor interface {
	Execute(ctx context.Context, req Request) (Result, error)
}

type LocalExecutor struct{}

type measurementComponent struct {
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}

type calculatorArgs struct {
	Operation       string                 `json:"operation,omitempty"`
	Expression      string                 `json:"expression,omitempty"`
	Input           []measurementComponent `json:"input,omitempty"`
	Per             []measurementComponent `json:"per,omitempty"`
	FromUnit        string                 `json:"from_unit,omitempty"`
	ToUnit          string                 `json:"to_unit,omitempty"`
	Weight          []measurementComponent `json:"weight,omitempty"`
	Height          []measurementComponent `json:"height,omitempty"`
	Distance        []measurementComponent `json:"distance,omitempty"`
	AgeYears        *float64               `json:"age_years,omitempty"`
	Gender          string                 `json:"gender,omitempty"`
	ActivityLevel   string                 `json:"activity_level,omitempty"`
	DurationSeconds *float64               `json:"duration_seconds,omitempty"`
	PaceUnit        string                 `json:"pace_unit,omitempty"`
	SpeedUnit       string                 `json:"speed_unit,omitempty"`
	Percent         *float64               `json:"percent,omitempty"`
	Value           *float64               `json:"value,omitempty"`
	Direction       string                 `json:"direction,omitempty"`
	Part            *float64               `json:"part,omitempty"`
	Whole           *float64               `json:"whole,omitempty"`
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

func (e *LocalExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	switch req.Action {
	case "get_time":
		return Result{
			Action: req.Action,
			Output: time.Now().Format(time.RFC3339),
		}, nil
	case "calculator":
		return executeCalculator(req)
	default:
		return Result{}, fmt.Errorf("unsupported tool action: %s", req.Action)
	}
}

func executeCalculator(req Request) (Result, error) {
	args, err := parseCalculatorArgs(req.Args)
	if err != nil {
		return Result{}, err
	}
	args = sanitizeCalculatorArgs(args)

	if expr := strings.TrimSpace(args.Expression); expr != "" {
		if args.Operation != "" && args.Operation != "expression" {
			return Result{}, fmt.Errorf("expression provided with non-expression operation")
		}
		value, err := evalExpression(expr)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: formatNumber(value),
		}, nil
	}

	switch strings.ToLower(strings.TrimSpace(args.Operation)) {
	case "expression":
		if strings.TrimSpace(args.Expression) == "" {
			return needsInputResult(req.Action, []string{"expression"}, "What expression should I calculate?"), nil
		}
		value, err := evalExpression(args.Expression)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: formatNumber(value),
		}, nil
	case "bmi":
		bmi, err := calculateBMI(args)
		if err != nil {
			if missing, question, ok := classifyCalculatorInputError(err); ok {
				return needsInputResult(req.Action, missing, question), nil
			}
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: fmt.Sprintf("BMI %.2f", bmi),
		}, nil
	case "bmr":
		bmr, err := calculateBMR(args)
		if err != nil {
			if missing, question, ok := classifyCalculatorInputError(err); ok {
				return needsInputResult(req.Action, missing, question), nil
			}
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: fmt.Sprintf("BMR %.2f kcal/day", bmr),
		}, nil
	case "tdee":
		tdee, err := calculateTDEE(args)
		if err != nil {
			if missing, question, ok := classifyCalculatorInputError(err); ok {
				return needsInputResult(req.Action, missing, question), nil
			}
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: fmt.Sprintf("TDEE %.2f kcal/day", tdee),
		}, nil
	case "convert":
		output, err := calculateConvert(args)
		if err != nil {
			if missing, question, ok := classifyCalculatorInputError(err); ok {
				return needsInputResult(req.Action, missing, question), nil
			}
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: output,
		}, nil
	case "pace":
		output, err := calculatePace(args)
		if err != nil {
			if missing, question, ok := classifyCalculatorInputError(err); ok {
				return needsInputResult(req.Action, missing, question), nil
			}
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: output,
		}, nil
	case "speed":
		output, err := calculateSpeed(args)
		if err != nil {
			if missing, question, ok := classifyCalculatorInputError(err); ok {
				return needsInputResult(req.Action, missing, question), nil
			}
			return Result{}, err
		}
		return Result{
			Action: req.Action,
			Output: output,
		}, nil
	case "percent_of":
		if args.Percent == nil || args.Value == nil {
			return needsInputResult(req.Action, []string{"percent", "value"}, "What percent and value should I use?"), nil
		}
		result := (*args.Percent / 100) * (*args.Value)
		return Result{
			Action: req.Action,
			Output: fmt.Sprintf("%s%% of %s = %s",
				formatNumber(*args.Percent),
				formatNumber(*args.Value),
				formatNumber(result),
			),
		}, nil
	case "percent_change":
		if args.Value == nil || args.Percent == nil {
			return needsInputResult(req.Action, []string{"value", "percent"}, "What value and percent should I use?"), nil
		}
		direction := strings.ToLower(strings.TrimSpace(args.Direction))
		if direction == "" {
			return needsInputResult(req.Action, []string{"direction"}, "Should the value increase or decrease?"), nil
		}
		factor := *args.Percent / 100
		result := *args.Value
		switch direction {
		case "increase":
			result = *args.Value * (1 + factor)
		case "decrease":
			result = *args.Value * (1 - factor)
		default:
			return Result{}, fmt.Errorf("percent_change direction must be increase or decrease")
		}
		return Result{
			Action: req.Action,
			Output: fmt.Sprintf("%s %s by %s%% = %s",
				formatNumber(*args.Value),
				map[string]string{
					"increase": "increased",
					"decrease": "decreased",
				}[direction],
				formatNumber(*args.Percent),
				formatNumber(result),
			),
		}, nil
	case "percent_ratio":
		if args.Part == nil || args.Whole == nil {
			return needsInputResult(req.Action, []string{"part", "whole"}, "What part and whole values should I use?"), nil
		}
		if *args.Whole == 0 {
			return Result{}, fmt.Errorf("percent_ratio whole must not be zero")
		}
		result := (*args.Part / *args.Whole) * 100
		return Result{
			Action: req.Action,
			Output: fmt.Sprintf("%s is %s%% of %s",
				formatNumber(*args.Part),
				formatNumber(result),
				formatNumber(*args.Whole),
			),
		}, nil
	default:
		return needsInputResult(req.Action, []string{"operation"}, "What kind of calculation do you want: expression, convert, BMI, BMR, TDEE, or percentage?"), nil
	}
}

func parseCalculatorArgs(raw json.RawMessage) (calculatorArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args calculatorArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return calculatorArgs{}, fmt.Errorf("invalid calculator args: %w", err)
	}
	return args, nil
}

func sanitizeCalculatorArgs(args calculatorArgs) calculatorArgs {
	args.Weight = filterMeasurementComponentsByCategory(args.Weight, measurementWeight)
	args.Height = filterMeasurementComponentsByCategory(args.Height, measurementLength)
	args.Distance = filterMeasurementComponentsByCategory(args.Distance, measurementLength)
	return args
}

func filterMeasurementComponentsByCategory(input []measurementComponent, expected measurementCategory) []measurementComponent {
	if len(input) == 0 {
		return nil
	}

	filtered := make([]measurementComponent, 0, len(input))
	for _, component := range input {
		unit := strings.ToLower(strings.TrimSpace(component.Unit))
		category, _, err := normalizeComponent(component.Value, unit)
		if err != nil || category != expected {
			continue
		}
		filtered = append(filtered, measurementComponent{
			Unit:  unit,
			Value: component.Value,
		})
	}
	return filtered
}

func calculateBMI(args calculatorArgs) (float64, error) {
	hasWeight := hasPositiveMeasurement(args.Weight)
	hasHeight := hasPositiveMeasurement(args.Height)
	if !hasWeight || !hasHeight {
		switch {
		case !hasWeight && !hasHeight:
			return 0, fmt.Errorf("bmi_missing:weight,height")
		case !hasWeight:
			return 0, fmt.Errorf("bmi_missing:weight")
		default:
			return 0, fmt.Errorf("bmi_missing:height")
		}
	}
	weightKg, weightCategory, _, err := normalizeMeasurement(args.Weight)
	if err != nil {
		return 0, err
	}
	if weightCategory != measurementWeight {
		return 0, fmt.Errorf("bmi weight must use weight units")
	}
	heightCm, heightCategory, _, err := normalizeMeasurement(args.Height)
	if err != nil {
		return 0, err
	}
	if heightCategory != measurementLength {
		return 0, fmt.Errorf("bmi height must use length units")
	}

	if weightKg <= 0 || heightCm <= 0 {
		return 0, fmt.Errorf("weight and height must be positive")
	}

	heightM := heightCm / 100
	return weightKg / (heightM * heightM), nil
}

func convertUnits(value float64, fromUnit, toUnit string) (float64, error) {
	fromUnit = strings.ToLower(strings.TrimSpace(fromUnit))
	toUnit = strings.ToLower(strings.TrimSpace(toUnit))
	if fromUnit == toUnit {
		return value, nil
	}

	if celsius, ok := tempToCelsius(value, fromUnit); ok {
		if converted, ok := celsiusToTemp(celsius, toUnit); ok {
			return converted, nil
		}
	}
	fromDef, ok := simpleUnitDefinition(fromUnit)
	if !ok {
		return 0, fmt.Errorf("unsupported conversion: %s to %s", fromUnit, toUnit)
	}
	toDef, ok := simpleUnitDefinition(toUnit)
	if !ok || !fromDef.Dims.equal(toDef.Dims) {
		return 0, fmt.Errorf("unsupported conversion: %s to %s", fromUnit, toUnit)
	}
	return value * fromDef.Factor / toDef.Factor, nil

}

type measurementCategory string

const (
	measurementLength measurementCategory = "length"
	measurementWeight measurementCategory = "weight"
	measurementTime   measurementCategory = "time"
	measurementVolume measurementCategory = "volume"
	measurementAmount measurementCategory = "amount"
	measurementTemp   measurementCategory = "temperature"
)

func normalizeMeasurement(input []measurementComponent) (float64, measurementCategory, string, error) {
	if len(input) == 0 {
		return 0, "", "", fmt.Errorf("measurement requires at least one component")
	}

	labelParts := make([]string, 0, len(input))
	var category measurementCategory
	var total float64

	for i, component := range input {
		unit := strings.ToLower(strings.TrimSpace(component.Unit))
		value := component.Value
		if value < 0 {
			return 0, "", "", fmt.Errorf("measurement values must be non-negative")
		}
		componentCategory, normalized, err := normalizeComponent(value, unit)
		if err != nil {
			return 0, "", "", err
		}
		if i == 0 {
			category = componentCategory
		} else if componentCategory != category {
			return 0, "", "", fmt.Errorf("measurement components must use compatible units")
		}
		if category == measurementTemp && len(input) > 1 {
			return 0, "", "", fmt.Errorf("temperature measurements cannot have multiple components")
		}
		total += normalized
		labelParts = append(labelParts, formatMeasurementComponent(component))
	}

	if total <= 0 {
		return 0, "", "", fmt.Errorf("measurement must be positive")
	}

	return total, category, strings.Join(labelParts, " "), nil
}

func formatMeasurementComponent(component measurementComponent) string {
	return fmt.Sprintf("%s %s",
		formatNumber(component.Value),
		strings.ToLower(strings.TrimSpace(component.Unit)),
	)
}

func FormatFactValue(kind string, raw json.RawMessage) (string, error) {
	switch strings.TrimSpace(kind) {
	case "measurement":
		var components []measurementComponent
		if err := json.Unmarshal(raw, &components); err != nil {
			return "", fmt.Errorf("invalid measurement observation: %w", err)
		}
		if len(components) == 0 {
			return "", fmt.Errorf("measurement observation is empty")
		}
		parts := make([]string, 0, len(components))
		for _, component := range components {
			parts = append(parts, formatMeasurementComponent(component))
		}
		return strings.Join(parts, " "), nil
	case "years":
		var years float64
		if err := json.Unmarshal(raw, &years); err != nil {
			return "", fmt.Errorf("invalid years observation: %w", err)
		}
		return formatNumber(years) + " years", nil
	case "text":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("invalid text observation: %w", err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("text observation is empty")
		}
		return value, nil
	case "enum":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("invalid enum observation: %w", err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("enum observation is empty")
		}
		return strings.ReplaceAll(value, "_", " "), nil
	default:
		return "", fmt.Errorf("unsupported fact kind: %s", kind)
	}
}

func formatNumber(value float64) string {
	const scale = 1000000000000
	rounded := math.Round(value*scale) / scale
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

func hasPositiveMeasurement(input []measurementComponent) bool {
	for _, component := range input {
		if component.Value > 0 {
			return true
		}
	}
	return false
}

func needsInputResult(action string, missing []string, question string) Result {
	return Result{
		Action:   action,
		Status:   "needs_input",
		Missing:  missing,
		Question: question,
	}
}

func classifyCalculatorInputError(err error) ([]string, string, bool) {
	msg := err.Error()
	switch msg {
	case "bmi_missing:height":
		return []string{"height"}, "What is the height?", true
	case "bmi_missing:weight":
		return []string{"weight"}, "What is the weight?", true
	case "bmi_missing:weight,height":
		return []string{"weight", "height"}, "What are the weight and height?", true
	case "bmr_missing:age_years":
		return []string{"age_years"}, "What is the age in years?", true
	case "bmr_missing:gender":
		return []string{"gender"}, "What gender should I use: male or female?", true
	case "bmr_missing:weight":
		return []string{"weight"}, "What is the weight?", true
	case "bmr_missing:height":
		return []string{"height"}, "What is the height?", true
	case "bmr_missing:age_years,gender":
		return []string{"age_years", "gender"}, "What are the age in years and gender?", true
	case "bmr_missing:age_years,weight":
		return []string{"age_years", "weight"}, "What are the age in years and weight?", true
	case "bmr_missing:age_years,height":
		return []string{"age_years", "height"}, "What are the age in years and height?", true
	case "bmr_missing:gender,weight":
		return []string{"gender", "weight"}, "What are the gender and weight?", true
	case "bmr_missing:gender,height":
		return []string{"gender", "height"}, "What are the gender and height?", true
	case "bmr_missing:weight,height":
		return []string{"weight", "height"}, "What are the weight and height?", true
	case "bmr_missing:age_years,gender,weight":
		return []string{"age_years", "gender", "weight"}, "What are the age in years, gender, and weight?", true
	case "bmr_missing:age_years,gender,height":
		return []string{"age_years", "gender", "height"}, "What are the age in years, gender, and height?", true
	case "bmr_missing:age_years,weight,height":
		return []string{"age_years", "weight", "height"}, "What are the age in years, weight, and height?", true
	case "bmr_missing:gender,weight,height":
		return []string{"gender", "weight", "height"}, "What are the gender, weight, and height?", true
	case "bmr_missing:age_years,gender,weight,height":
		return []string{"age_years", "gender", "weight", "height"}, "What are the age in years, gender, weight, and height?", true
	case "tdee_missing:activity_level":
		return []string{"activity_level"}, "What is the activity level: sedentary, light, moderate, active, or very_active?", true
	case "tdee_missing:age_years":
		return []string{"age_years"}, "What is the age in years?", true
	case "tdee_missing:gender":
		return []string{"gender"}, "What gender should I use: male or female?", true
	case "tdee_missing:weight":
		return []string{"weight"}, "What is the weight?", true
	case "tdee_missing:height":
		return []string{"height"}, "What is the height?", true
	case "convert_missing:input_or_value":
		return []string{"input"}, "What value should I convert?", true
	case "convert_missing:from_unit":
		return []string{"from_unit"}, "What unit is the value in?", true
	case "convert_missing:to_unit":
		return []string{"to_unit"}, "What unit should I convert to?", true
	case "convert_missing:per":
		return []string{"per"}, "What denominator value should I use?", true
	case "pace_missing:distance":
		return []string{"distance"}, "What distance should I use?", true
	case "pace_missing:duration_seconds":
		return []string{"duration_seconds"}, "What duration should I use?", true
	case "pace_missing:pace_unit":
		return []string{"pace_unit"}, "What pace unit should I use: min_per_km or min_per_mile?", true
	case "speed_missing:distance":
		return []string{"distance"}, "What distance should I use?", true
	case "speed_missing:duration_seconds":
		return []string{"duration_seconds"}, "What duration should I use?", true
	case "speed_missing:speed_unit":
		return []string{"speed_unit"}, "What speed unit should I use: km_h, mph, or m_s?", true
	default:
		if missing, question, ok := classifyStructuredMissing(msg, "bmr_missing:"); ok {
			return missing, question, true
		}
		if missing, question, ok := classifyStructuredMissing(msg, "tdee_missing:"); ok {
			return missing, question, true
		}
		return nil, "", false
	}
}

func classifyStructuredMissing(msg, prefix string) ([]string, string, bool) {
	if !strings.HasPrefix(msg, prefix) {
		return nil, "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	if body == "" {
		return nil, "", false
	}
	rawFields := strings.Split(body, ",")
	fields := make([]string, 0, len(rawFields))
	for _, raw := range rawFields {
		field := strings.TrimSpace(raw)
		if field == "" {
			continue
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return nil, "", false
	}
	return fields, buildMissingQuestion(fields), true
}

func buildMissingQuestion(fields []string) string {
	labels := make([]string, 0, len(fields))
	for _, field := range fields {
		labels = append(labels, missingFieldLabel(field))
	}
	if len(labels) == 1 {
		return "What is the " + labels[0] + "?"
	}
	return "What are the " + joinQuestionLabels(labels) + "?"
}

func ClarificationQuestion(fields []string) string {
	return buildMissingQuestion(fields)
}

func missingFieldLabel(field string) string {
	switch field {
	case "age_years":
		return "age in years"
	case "activity_level":
		return "activity level"
	default:
		return strings.ReplaceAll(field, "_", " ")
	}
}

func joinQuestionLabels(labels []string) string {
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return labels[0]
	case 2:
		return labels[0] + " and " + labels[1]
	default:
		return strings.Join(labels[:len(labels)-1], ", ") + ", and " + labels[len(labels)-1]
	}
}

func calculateBMR(args calculatorArgs) (float64, error) {
	missing := make([]string, 0, 4)
	if args.AgeYears == nil {
		missing = append(missing, "age_years")
	}
	if strings.TrimSpace(args.Gender) == "" {
		missing = append(missing, "gender")
	}
	if !hasPositiveMeasurement(args.Weight) {
		missing = append(missing, "weight")
	}
	if !hasPositiveMeasurement(args.Height) {
		missing = append(missing, "height")
	}
	if len(missing) > 0 {
		return 0, fmt.Errorf("bmr_missing:%s", strings.Join(missing, ","))
	}

	weightKg, weightCategory, _, err := normalizeMeasurement(args.Weight)
	if err != nil {
		return 0, err
	}
	if weightCategory != measurementWeight {
		return 0, fmt.Errorf("bmr weight must use weight units")
	}
	heightCm, heightCategory, _, err := normalizeMeasurement(args.Height)
	if err != nil {
		return 0, err
	}
	if heightCategory != measurementLength {
		return 0, fmt.Errorf("bmr height must use length units")
	}

	ageYears := *args.AgeYears
	if ageYears <= 0 {
		return 0, fmt.Errorf("age_years must be positive")
	}

	gender := strings.ToLower(strings.TrimSpace(args.Gender))
	var adjustment float64
	switch gender {
	case "male":
		adjustment = 5
	case "female":
		adjustment = -161
	default:
		return 0, fmt.Errorf("gender must be male or female")
	}

	return 10*weightKg + 6.25*heightCm - 5*ageYears + adjustment, nil
}

func calculateTDEE(args calculatorArgs) (float64, error) {
	bmr, err := calculateBMR(args)
	if err != nil {
		if strings.HasPrefix(err.Error(), "bmr_missing:") {
			return 0, fmt.Errorf("tdee_missing:%s", strings.TrimPrefix(err.Error(), "bmr_missing:"))
		}
		return 0, err
	}

	level := strings.ToLower(strings.TrimSpace(args.ActivityLevel))
	if level == "" {
		return 0, fmt.Errorf("tdee_missing:activity_level")
	}

	var factor float64
	switch level {
	case "sedentary":
		factor = 1.2
	case "light":
		factor = 1.375
	case "moderate":
		factor = 1.55
	case "active":
		factor = 1.725
	case "very_active":
		factor = 1.9
	default:
		return 0, fmt.Errorf("activity_level must be sedentary, light, moderate, active, or very_active")
	}

	return bmr * factor, nil
}

func calculateConvert(args calculatorArgs) (string, error) {
	toUnit := strings.TrimSpace(args.ToUnit)
	if toUnit == "" {
		return "", fmt.Errorf("convert_missing:to_unit")
	}

	if args.Value != nil || strings.TrimSpace(args.FromUnit) != "" {
		if args.Value == nil {
			return "", fmt.Errorf("convert_missing:input_or_value")
		}
		if strings.TrimSpace(args.FromUnit) == "" {
			return "", fmt.Errorf("convert_missing:from_unit")
		}
		converted, fromCanonical, toCanonical, err := convertExpressionValue(*args.Value, args.FromUnit, toUnit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s = %s %s",
			formatNumber(*args.Value),
			fromCanonical,
			formatNumber(converted),
			toCanonical,
		), nil
	}

	if len(args.Input) == 0 {
		return "", fmt.Errorf("convert_missing:input_or_value")
	}

	inputValue, inputCategory, inputLabel, err := normalizeMeasurement(args.Input)
	if err != nil {
		return "", err
	}
	inputDims, ok := dimensionsForCategory(inputCategory)
	if !ok {
		return "", fmt.Errorf("unsupported conversion category: %s", inputCategory)
	}

	baseValue := inputValue
	fromDims := inputDims
	label := inputLabel

	if len(args.Per) > 0 {
		perValue, perCategory, perLabel, err := normalizeMeasurement(args.Per)
		if err != nil {
			return "", err
		}
		if perValue <= 0 {
			return "", fmt.Errorf("convert_missing:per")
		}
		perDims, ok := dimensionsForCategory(perCategory)
		if !ok {
			return "", fmt.Errorf("unsupported conversion category: %s", perCategory)
		}
		baseValue = inputValue / perValue
		fromDims = inputDims.subtract(perDims)
		label = fmt.Sprintf("%s / %s", inputLabel, perLabel)
	}

	converted, toCanonical, err := convertBaseValueToExpression(baseValue, fromDims, toUnit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s = %s %s",
		label,
		formatNumber(converted),
		toCanonical,
	), nil
}

func calculatePace(args calculatorArgs) (string, error) {
	if len(args.Distance) == 0 {
		return "", fmt.Errorf("pace_missing:distance")
	}
	if args.DurationSeconds == nil {
		return "", fmt.Errorf("pace_missing:duration_seconds")
	}
	if strings.TrimSpace(args.PaceUnit) == "" {
		return "", fmt.Errorf("pace_missing:pace_unit")
	}
	if *args.DurationSeconds <= 0 {
		return "", fmt.Errorf("duration_seconds must be positive")
	}

	target := ""
	display := ""
	switch strings.ToLower(strings.TrimSpace(args.PaceUnit)) {
	case "min_per_km":
		target = "min/km"
		display = "min/km"
	case "min_per_mile":
		target = "min/mi"
		display = "min/mile"
	default:
		return "", fmt.Errorf("pace_unit must be min_per_km or min_per_mile")
	}

	distanceValue, category, _, err := normalizeMeasurement(args.Distance)
	if err != nil {
		return "", err
	}
	if category != measurementLength {
		return "", fmt.Errorf("pace distance must use length units")
	}
	distanceDims, _ := dimensionsForCategory(category)
	baseValue := distanceValue / *args.DurationSeconds
	converted, _, err := convertBaseValueToExpression(baseValue, distanceDims.subtract(unitDimensions{Time: 1}), target)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Pace %s %s", formatNumber(converted), display), nil
}

func calculateSpeed(args calculatorArgs) (string, error) {
	if len(args.Distance) == 0 {
		return "", fmt.Errorf("speed_missing:distance")
	}
	if args.DurationSeconds == nil {
		return "", fmt.Errorf("speed_missing:duration_seconds")
	}
	if strings.TrimSpace(args.SpeedUnit) == "" {
		return "", fmt.Errorf("speed_missing:speed_unit")
	}
	if *args.DurationSeconds <= 0 {
		return "", fmt.Errorf("duration_seconds must be positive")
	}

	target := ""
	display := ""
	switch strings.ToLower(strings.TrimSpace(args.SpeedUnit)) {
	case "km_h":
		target = "km/hr"
		display = "km/h"
	case "mph":
		target = "mi/hr"
		display = "mph"
	case "m_s":
		target = "m/s"
		display = "m/s"
	default:
		return "", fmt.Errorf("speed_unit must be km_h, mph, or m_s")
	}

	distanceValue, category, _, err := normalizeMeasurement(args.Distance)
	if err != nil {
		return "", err
	}
	if category != measurementLength {
		return "", fmt.Errorf("speed distance must use length units")
	}
	distanceDims, _ := dimensionsForCategory(category)
	baseValue := distanceValue / *args.DurationSeconds
	converted, _, err := convertBaseValueToExpression(baseValue, distanceDims.subtract(unitDimensions{Time: 1}), target)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Speed %s %s", formatNumber(converted), display), nil
}

func normalizeComponent(value float64, unit string) (measurementCategory, float64, error) {
	if celsius, ok := tempToCelsius(value, unit); ok {
		return measurementTemp, celsius, nil
	}
	if def, ok := simpleUnitDefinition(unit); ok {
		return def.Category, value * def.Factor, nil
	}
	return "", 0, fmt.Errorf("unsupported measurement unit: %s", unit)
}

func convertNormalized(value float64, category measurementCategory, toUnit string) (float64, error) {
	switch category {
	case measurementLength:
		return convertUnits(value, "cm", toUnit)
	case measurementWeight:
		return convertUnits(value, "kg", toUnit)
	case measurementTime:
		return convertUnits(value, "s", toUnit)
	case measurementVolume:
		return convertUnits(value, "l", toUnit)
	case measurementAmount:
		return convertUnits(value, "mol", toUnit)
	case measurementTemp:
		return convertUnits(value, "c", toUnit)
	default:
		return 0, fmt.Errorf("unsupported measurement category: %s", category)
	}
}

func tempToCelsius(value float64, unit string) (float64, bool) {
	switch unit {
	case "c":
		return value, true
	case "f":
		return (value - 32) * 5 / 9, true
	case "k":
		return value - 273.15, true
	default:
		return 0, false
	}
}

func celsiusToTemp(value float64, unit string) (float64, bool) {
	switch unit {
	case "c":
		return value, true
	case "f":
		return value*9/5 + 32, true
	case "k":
		return value + 273.15, true
	default:
		return 0, false
	}
}

func evalExpression(input string) (float64, error) {
	p := &exprParser{input: strings.ReplaceAll(input, " ", "")}
	value, err := p.parseExpression()
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.input) {
		return 0, fmt.Errorf("unexpected token at position %d", p.pos)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("invalid arithmetic result")
	}
	return value, nil
}

type exprParser struct {
	input string
	pos   int
}

func (p *exprParser) parseExpression() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for p.pos < len(p.input) {
		switch p.input[p.pos] {
		case '+':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left += right
		case '-':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left -= right
		default:
			return left, nil
		}
	}
	return left, nil
}

func (p *exprParser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for p.pos < len(p.input) {
		switch p.input[p.pos] {
		case '*':
			p.pos++
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			left *= right
		case '/':
			p.pos++
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		default:
			return left, nil
		}
	}
	return left, nil
}

func (p *exprParser) parseFactor() (float64, error) {
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	if p.input[p.pos] == '(' {
		p.pos++
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return value, nil
	}

	if p.input[p.pos] == '-' {
		p.pos++
		value, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		return -value, nil
	}

	start := p.pos
	dotSeen := false
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch >= '0' && ch <= '9' {
			p.pos++
			continue
		}
		if ch == '.' && !dotSeen {
			dotSeen = true
			p.pos++
			continue
		}
		break
	}
	if start == p.pos {
		return 0, fmt.Errorf("expected number at position %d", p.pos)
	}
	return strconv.ParseFloat(p.input[start:p.pos], 64)
}
