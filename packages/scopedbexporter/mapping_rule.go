/*
 * Copyright 2026 ScopeDB, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package scopedbexporter

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/confmap"
	"go.yaml.in/yaml/v3"
)

// MappingConfig projects destination columns from one OpenTelemetry record.
type MappingConfig map[string]MappingRule

// MappingRule is either a shorthand source selector or an expanded projection
// rule. Expanded rules select the first present source or a constant, optionally
// fall back to a default, and then cast the result.
type MappingRule struct {
	Source  string   `mapstructure:"source" yaml:"source,omitempty"`
	Sources []string `mapstructure:"sources" yaml:"sources,omitempty"`
	Cast    string   `mapstructure:"cast" yaml:"cast,omitempty"`
	Default any      `mapstructure:"default" yaml:"default,omitempty"`
	Value   any      `mapstructure:"value" yaml:"value,omitempty"`

	sourceSet  bool
	sourcesSet bool
	defaultSet bool
	valueSet   bool
}

type rawMappingRule struct {
	Source  string   `mapstructure:"source" yaml:"source,omitempty"`
	Sources []string `mapstructure:"sources" yaml:"sources,omitempty"`
	Cast    string   `mapstructure:"cast" yaml:"cast,omitempty"`
	Default any      `mapstructure:"default" yaml:"default,omitempty"`
	Value   any      `mapstructure:"value" yaml:"value,omitempty"`
}

func (r *MappingRule) UnmarshalText(text []byte) error {
	*r = MappingRule{Source: string(text), sourceSet: true}
	return nil
}

func (r *MappingRule) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if node.Kind == yaml.ScalarNode {
		if node.Tag != "!!str" {
			return fmt.Errorf("mapping shorthand must be a source selector string")
		}
		return r.UnmarshalText([]byte(node.Value))
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("mapping rule must be a source selector string or an object")
	}

	allowed := map[string]bool{"source": true, "sources": true, "cast": true, "default": true, "value": true}
	seen := make(map[string]bool, len(allowed))
	for index := 0; index < len(node.Content); index += 2 {
		name := node.Content[index].Value
		if !allowed[name] {
			return fmt.Errorf("unknown mapping rule field %q", name)
		}
		if seen[name] {
			return fmt.Errorf("mapping rule field %q is repeated", name)
		}
		seen[name] = true
	}

	var raw rawMappingRule
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*r = MappingRule{
		Source:     raw.Source,
		Sources:    raw.Sources,
		Cast:       raw.Cast,
		Default:    raw.Default,
		Value:      raw.Value,
		sourceSet:  seen["source"],
		sourcesSet: seen["sources"],
		defaultSet: seen["default"],
		valueSet:   seen["value"],
	}
	return nil
}

func (r *MappingRule) Unmarshal(conf *confmap.Conf) error {
	var raw rawMappingRule
	if err := conf.Unmarshal(&raw); err != nil {
		return err
	}
	*r = MappingRule{
		Source:     raw.Source,
		Sources:    raw.Sources,
		Cast:       raw.Cast,
		Default:    raw.Default,
		Value:      raw.Value,
		sourceSet:  conf.IsSet("source"),
		sourcesSet: conf.IsSet("sources"),
		defaultSet: conf.IsSet("default"),
		valueSet:   conf.IsSet("value"),
	}
	return nil
}

func (r MappingRule) MarshalYAML() (any, error) {
	rule, err := r.normalized()
	if err != nil {
		return nil, err
	}
	if rule.Source != "" && len(rule.Sources) == 0 && rule.Cast == "" && !rule.hasDefault() {
		return rule.Source, nil
	}
	return rawMappingRule{
		Source:  rule.Source,
		Sources: rule.Sources,
		Cast:    rule.Cast,
		Default: rule.Default,
		Value:   rule.Value,
	}, nil
}

func (r MappingRule) hasDefault() bool {
	return r.defaultSet || r.Default != nil
}

func (r MappingRule) hasSource() bool {
	return r.sourceSet || r.Source != ""
}

func (r MappingRule) hasSources() bool {
	return r.sourcesSet || r.Sources != nil
}

func (r MappingRule) hasValue() bool {
	return r.valueSet || r.Value != nil
}

func (r MappingRule) normalized() (MappingRule, error) {
	r.Source = strings.TrimSpace(r.Source)
	r.Cast = strings.ToLower(strings.TrimSpace(r.Cast))
	r.Sources = append([]string(nil), r.Sources...)
	for index := range r.Sources {
		r.Sources[index] = strings.TrimSpace(r.Sources[index])
		if r.Sources[index] == "" {
			return MappingRule{}, fmt.Errorf("sources[%d] is empty", index)
		}
	}
	if r.hasSource() && r.Source == "" {
		return MappingRule{}, fmt.Errorf("source is empty")
	}
	if r.hasSources() && len(r.Sources) == 0 {
		return MappingRule{}, fmt.Errorf("sources must contain at least one selector")
	}
	if r.hasSource() && r.hasSources() {
		return MappingRule{}, fmt.Errorf("source and sources cannot be used together")
	}
	if !r.hasSource() && !r.hasSources() && !r.hasValue() {
		return MappingRule{}, fmt.Errorf("source, sources, or value is required")
	}
	if r.hasValue() && (r.hasSource() || r.hasSources() || r.hasDefault()) {
		return MappingRule{}, fmt.Errorf("value cannot be combined with source, sources, or default")
	}
	if r.hasDefault() && r.Default == nil {
		return MappingRule{}, fmt.Errorf("default cannot be null; null values are omitted")
	}
	if r.hasValue() && r.Value == nil {
		return MappingRule{}, fmt.Errorf("value cannot be null; null values are omitted")
	}
	if r.Cast != "" && !supportedMappingCast(r.Cast) {
		return MappingRule{}, fmt.Errorf("unsupported cast %q; choose string, int, uint, float, boolean, or timestamp", r.Cast)
	}
	if r.hasDefault() {
		if _, err := json.Marshal(r.Default); err != nil {
			return MappingRule{}, fmt.Errorf("default is not JSON-compatible: %w", err)
		}
	}
	if r.hasValue() {
		if _, err := json.Marshal(r.Value); err != nil {
			return MappingRule{}, fmt.Errorf("value is not JSON-compatible: %w", err)
		}
	}
	return r, nil
}

func (r MappingRule) description() string {
	rule, err := r.normalized()
	if err != nil {
		return ""
	}
	var expression string
	if rule.hasValue() {
		expression = "value(" + mappingLiteralDescription(rule.Value) + ")"
	} else {
		sources := append([]string(nil), rule.Sources...)
		if rule.Source != "" {
			sources = []string{rule.Source}
		}
		if rule.hasDefault() {
			sources = append(sources, "default("+mappingLiteralDescription(rule.Default)+")")
		}
		expression = strings.Join(sources, " -> ")
	}
	if rule.Cast != "" {
		expression += " | cast=" + rule.Cast
	}
	return expression
}

func mappingLiteralDescription(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

const mappingResolutionMissing = -1

type mappingEvaluation struct {
	value      any
	present    bool
	resolution int
}

type mappingEvaluator func(Record) (mappingEvaluation, error)

type compiledMappingRule struct {
	description      string
	valueType        selectorType
	runtimeDependent bool
	resolutions      []string
	evaluate         mappingEvaluator
}

func compileMappingRule(signal string, configured MappingRule) (compiledMappingRule, error) {
	rule, err := configured.normalized()
	if err != nil {
		return compiledMappingRule{}, err
	}

	value := rule.Value
	defaultValue := rule.Default
	var getters []recordGetter
	var resolutions []string
	var sourceTypes []selectorType
	var inputTypes []selectorType
	if rule.hasValue() {
		inputTypes = []selectorType{selectorTypeForValue(value)}
		resolutions = []string{"value"}
	} else {
		sources := rule.Sources
		if rule.Source != "" {
			sources = []string{rule.Source}
		}
		resolutions = append([]string(nil), sources...)
		getters = make([]recordGetter, 0, len(sources))
		sourceTypes = make([]selectorType, 0, len(sources))
		inputTypes = make([]selectorType, 0, len(sources)+1)
		for _, source := range sources {
			getter, err := compileRecordGetter(signal, source)
			if err != nil {
				return compiledMappingRule{}, err
			}
			getters = append(getters, getter)
			inputType := selectorTypeFor(source)
			inputTypes = append(inputTypes, inputType)
			sourceTypes = append(sourceTypes, inputType)
		}
		if rule.hasDefault() {
			inputTypes = append(inputTypes, selectorTypeForValue(defaultValue))
			resolutions = append(resolutions, "default")
		}
	}

	valueType := mergedSelectorType(inputTypes)
	runtimeDependent := valueType.runtimeDependent()
	var targetType selectorType
	if rule.Cast != "" {
		targetType = selectorType(rule.Cast)
		for _, inputType := range inputTypes {
			if !mappingCastPossible(inputType, targetType) {
				return compiledMappingRule{}, fmt.Errorf("cannot cast %s to %s", inputType, targetType)
			}
		}
		if rule.hasValue() {
			value, err = castMappingValue(value, targetType)
			if err != nil {
				return compiledMappingRule{}, fmt.Errorf("value: %w", mappingConversionError(rule.Value, targetType))
			}
		}
		if rule.hasDefault() {
			defaultValue, err = castMappingValue(defaultValue, targetType)
			if err != nil {
				return compiledMappingRule{}, fmt.Errorf("default: %w", mappingConversionError(rule.Default, targetType))
			}
		}
		valueType = targetType
		runtimeDependent = false
		for _, inputType := range sourceTypes {
			if mappingCastNeedsRuntimeCheck(inputType, targetType) {
				runtimeDependent = true
				break
			}
		}
	}

	var evaluator mappingEvaluator
	if rule.hasValue() {
		evaluator = func(Record) (mappingEvaluation, error) {
			return mappingEvaluation{value: value, present: true, resolution: 0}, nil
		}
	} else {
		evaluator = func(record Record) (mappingEvaluation, error) {
			for index, getter := range getters {
				if selected, ok := getter(record); ok {
					return mappingEvaluation{value: selected, present: true, resolution: index}, nil
				}
			}
			return mappingEvaluation{resolution: mappingResolutionMissing}, nil
		}
		if rule.Cast != "" {
			inner := evaluator
			evaluator = func(record Record) (mappingEvaluation, error) {
				evaluation, err := inner(record)
				if err != nil || !evaluation.present {
					return evaluation, err
				}
				cast, err := castMappingValue(evaluation.value, targetType)
				if err != nil {
					return evaluation, &mappingError{
						reason: mappingReasonCastFailed,
						err:    mappingConversionError(evaluation.value, targetType),
					}
				}
				evaluation.value = cast
				return evaluation, nil
			}
		}
		if rule.hasDefault() {
			inner := evaluator
			evaluator = func(record Record) (mappingEvaluation, error) {
				evaluation, err := inner(record)
				if err != nil || evaluation.present {
					return evaluation, err
				}
				return mappingEvaluation{
					value:      defaultValue,
					present:    true,
					resolution: len(getters),
				}, nil
			}
		}
	}

	return compiledMappingRule{
		description:      rule.description(),
		valueType:        valueType,
		runtimeDependent: runtimeDependent,
		resolutions:      resolutions,
		evaluate:         evaluator,
	}, nil
}

func mappingConversionError(value any, target selectorType) error {
	return fmt.Errorf(
		"%s value %s cannot be converted to %s",
		selectorTypeForValue(value),
		mappingErrorValueDescription(value),
		target,
	)
}

func mappingErrorValueDescription(value any) string {
	const maxRunes = 96
	description := []rune(mappingLiteralDescription(value))
	if len(description) <= maxRunes {
		return string(description)
	}
	return string(description[:maxRunes-3]) + "..."
}

func supportedMappingCast(value string) bool {
	switch selectorType(value) {
	case selectorTypeString, selectorTypeInt, selectorTypeUInt, selectorTypeFloat,
		selectorTypeBoolean, selectorTypeTimestamp:
		return true
	default:
		return false
	}
}

func mergedSelectorType(types []selectorType) selectorType {
	if len(types) == 0 {
		return selectorTypeDynamic
	}
	result := types[0]
	for _, valueType := range types[1:] {
		if valueType != result {
			return selectorTypeDynamic
		}
	}
	return result
}

func selectorTypeForValue(value any) selectorType {
	switch value.(type) {
	case string:
		return selectorTypeString
	case time.Time:
		return selectorTypeTimestamp
	case bool:
		return selectorTypeBoolean
	case int, int8, int16, int32, int64:
		return selectorTypeInt
	case uint, uint8, uint16, uint32, uint64:
		return selectorTypeUInt
	case float32, float64:
		return selectorTypeFloat
	case map[string]any, Record:
		return selectorTypeObject
	case []any, []map[string]any:
		return selectorTypeArray
	}
	valueType := reflect.TypeOf(value)
	if valueType != nil {
		switch valueType.Kind() {
		case reflect.Map:
			return selectorTypeObject
		case reflect.Array, reflect.Slice:
			return selectorTypeArray
		}
	}
	return selectorTypeDynamic
}

func mappingCastPossible(source selectorType, target selectorType) bool {
	if source == selectorTypeDynamic || source == selectorTypeNumber || source == target {
		return true
	}
	if target == selectorTypeString {
		return true
	}
	switch target {
	case selectorTypeInt, selectorTypeUInt, selectorTypeFloat:
		return source == selectorTypeString || source == selectorTypeInt || source == selectorTypeUInt || source == selectorTypeFloat
	case selectorTypeBoolean:
		return source == selectorTypeString || source == selectorTypeBoolean
	case selectorTypeTimestamp:
		return source == selectorTypeString || source == selectorTypeInt || source == selectorTypeUInt || source == selectorTypeTimestamp
	default:
		return false
	}
}

func mappingCastNeedsRuntimeCheck(source selectorType, target selectorType) bool {
	if source == target {
		return source == selectorTypeFloat
	}
	if source == selectorTypeDynamic || source == selectorTypeNumber {
		return true
	}
	switch target {
	case selectorTypeString:
		return source == selectorTypeFloat || source == selectorTypeObject || source == selectorTypeArray
	case selectorTypeInt:
		return source != selectorTypeInt
	case selectorTypeUInt:
		return source != selectorTypeUInt
	case selectorTypeFloat:
		return source == selectorTypeString
	case selectorTypeBoolean:
		return source != selectorTypeBoolean
	case selectorTypeTimestamp:
		return source != selectorTypeTimestamp && source != selectorTypeInt
	default:
		return true
	}
}

func castMappingValue(value any, target selectorType) (any, error) {
	switch target {
	case selectorTypeString:
		return mappingString(value)
	case selectorTypeInt:
		return mappingInt(value)
	case selectorTypeUInt:
		return mappingUInt(value)
	case selectorTypeFloat:
		return mappingFloat(value)
	case selectorTypeBoolean:
		return mappingBoolean(value)
	case selectorTypeTimestamp:
		return mappingTimestamp(value)
	default:
		return nil, fmt.Errorf("unsupported cast %q", target)
	}
}

func mappingString(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	if timestamp, ok := value.(time.Time); ok {
		return timestamp.UTC().Format(time.RFC3339Nano), nil
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() {
		switch reflected.Kind() {
		case reflect.Bool:
			return strconv.FormatBool(reflected.Bool()), nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(reflected.Int(), 10), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.FormatUint(reflected.Uint(), 10), nil
		case reflect.Float32, reflect.Float64:
			floatValue := reflected.Float()
			if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
				return "", fmt.Errorf("non-finite float")
			}
			return strconv.FormatFloat(floatValue, 'g', -1, reflected.Type().Bits()), nil
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func mappingInt(value any) (int64, error) {
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return 0, fmt.Errorf("expected a number or numeric string")
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uintToInt(reflected.Uint())
	case reflect.Float32, reflect.Float64:
		return floatToInt(reflected.Float())
	default:
		return 0, fmt.Errorf("expected a number or numeric string")
	}
}

func uintToInt(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("value exceeds int64")
	}
	return int64(value), nil
}

func floatToInt(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < math.MinInt64 || value >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("value is not an int64")
	}
	return int64(value), nil
}

func mappingUInt(value any) (uint64, error) {
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseUint(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return 0, fmt.Errorf("expected a number or numeric string")
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intToUInt(reflected.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflected.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return floatToUInt(reflected.Float())
	default:
		return 0, fmt.Errorf("expected a number or numeric string")
	}
}

func intToUInt(value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("value is negative")
	}
	return uint64(value), nil
}

func floatToUInt(value float64) (uint64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < 0 || value >= float64(math.MaxUint64) {
		return 0, fmt.Errorf("value is not a uint64")
	}
	return uint64(value), nil
}

func mappingFloat(value any) (float64, error) {
	var result float64
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return 0, err
		}
		result = parsed
	} else {
		reflected := reflect.ValueOf(value)
		if !reflected.IsValid() {
			return 0, fmt.Errorf("expected a number or numeric string")
		}
		switch reflected.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = float64(reflected.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			result = float64(reflected.Uint())
		case reflect.Float32, reflect.Float64:
			result = reflected.Float()
		default:
			return 0, fmt.Errorf("expected a number or numeric string")
		}
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, fmt.Errorf("non-finite float")
	}
	return result, nil
}

func mappingBoolean(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, err
		}
		return parsed, nil
	default:
		return false, fmt.Errorf("expected a boolean or boolean string")
	}
}

func mappingTimestamp(value any) (string, error) {
	if timestamp, ok := value.(time.Time); ok {
		return timestamp.UTC().Format(time.RFC3339Nano), nil
	}
	if textValue, ok := value.(string); ok {
		text := strings.TrimSpace(textValue)
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano), nil
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return "", fmt.Errorf("expected RFC3339 or Unix nanoseconds")
		}
		return time.Unix(0, parsed).UTC().Format(time.RFC3339Nano), nil
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return "", fmt.Errorf("expected RFC3339 or Unix nanoseconds")
	}
	var nanos int64
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		nanos = reflected.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		converted, err := uintToInt(reflected.Uint())
		if err != nil {
			return "", err
		}
		nanos = converted
	default:
		return "", fmt.Errorf("expected RFC3339 or Unix nanoseconds")
	}
	return time.Unix(0, nanos).UTC().Format(time.RFC3339Nano), nil
}
