package client

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
)

// ErrorBodyShape describes only the bounded response's JSON shape. It never
// includes field names supplied by the server or any response values.
type ErrorBodyShape string

const (
	BodyShapeUnclassified ErrorBodyShape = "unclassified"
	BodyShapeEmpty        ErrorBodyShape = "empty"
	BodyShapeOversized    ErrorBodyShape = "oversized"
	BodyShapeMalformed    ErrorBodyShape = "malformed_json"
	BodyShapeNonObject    ErrorBodyShape = "json_non_object"
	BodyShapeObject       ErrorBodyShape = "json_object"
)

func safeErrorBodyShape(shape ErrorBodyShape) ErrorBodyShape {
	switch shape {
	case BodyShapeEmpty, BodyShapeOversized, BodyShapeMalformed, BodyShapeNonObject, BodyShapeObject:
		return shape
	default:
		return BodyShapeUnclassified
	}
}

// ErrorReason is an allowlisted interpretation of a structured API error code.
// It and the hint booleans describe response evidence, not a confirmed cause.
type ErrorReason string

const (
	ReasonUnclassified   ErrorReason = "unclassified"
	ReasonValidation     ErrorReason = "validation"
	ReasonAuthentication ErrorReason = "authentication"
	ReasonAuthorization  ErrorReason = "authorization"
	ReasonRateLimit      ErrorReason = "rate_limit"
	ReasonQuotaOrPlan    ErrorReason = "quota_or_plan"
	ReasonConflict       ErrorReason = "conflict"
)

func safeErrorReason(reason ErrorReason) ErrorReason {
	switch reason {
	case ReasonValidation, ReasonAuthentication, ReasonAuthorization, ReasonRateLimit, ReasonQuotaOrPlan, ReasonConflict:
		return reason
	default:
		return ReasonUnclassified
	}
}

func newAPIError(status int, body []byte) *APIError {
	result := &APIError{StatusCode: status, Reason: ReasonUnclassified, BodyShape: BodyShapeUnclassified}
	if len(body) > maxErrorBodyBytes {
		result.BodyShape = BodyShapeOversized
		return result
	}
	body = bytes.Trim(body, " \t\r\n")
	if len(body) == 0 {
		result.BodyShape = BodyShapeEmpty
		return result
	}
	if !json.Valid(body) {
		result.BodyShape = BodyShapeMalformed
		return result
	}
	if body[0] != '{' {
		result.BodyShape = BodyShapeNonObject
		return result
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		result.BodyShape = BodyShapeMalformed
		return result
	}
	result.BodyShape = BodyShapeObject
	inspectErrorObject(result, object, true)
	return result
}

// Only known message/error/code shapes are inspected, and no response strings
// are retained. Unknown fields, codes, malformed JSON and oversized bodies are
// deliberately unclassified. Presence flags cover only these known keys in the
// root or one nested error object; they say nothing about the values' validity.
func inspectErrorObject(result *APIError, object map[string]json.RawMessage, nested bool) {
	_, hasMessage := object["message"]
	_, hasError := object["error"]
	_, hasCode := object["code"]
	result.MessageFieldPresent = result.MessageFieldPresent || hasMessage
	result.ErrorFieldPresent = result.ErrorFieldPresent || hasError
	result.CodeFieldPresent = result.CodeFieldPresent || hasCode
	var code string
	if json.Unmarshal(object["code"], &code) == nil {
		addErrorHints(result, code)
		if result.Reason == ReasonUnclassified {
			result.Reason = reasonForCode(code)
		}
	}
	for _, field := range []string{"message", "error"} {
		var message string
		if json.Unmarshal(object[field], &message) == nil {
			addErrorHints(result, message)
		}
	}
	if nested {
		var errorObject map[string]json.RawMessage
		if json.Unmarshal(object["error"], &errorObject) == nil && errorObject != nil {
			result.NestedErrorObject = true
			inspectErrorObject(result, errorObject, false)
		}
	}
}

func reasonForCode(code string) ErrorReason {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "INVALID_ARGUMENT", "INVALID_REQUEST", "VALIDATION_ERROR":
		return ReasonValidation
	case "UNAUTHORIZED", "UNAUTHENTICATED", "INVALID_TOKEN", "TOKEN_EXPIRED":
		return ReasonAuthentication
	case "FORBIDDEN", "PERMISSION_DENIED":
		return ReasonAuthorization
	case "RATE_LIMITED", "RATE_LIMIT_EXCEEDED", "TOO_MANY_REQUESTS":
		return ReasonRateLimit
	case "QUOTA_EXCEEDED", "LIMIT_EXCEEDED", "PLAN_LIMIT_EXCEEDED":
		return ReasonQuotaOrPlan
	case "CONFLICT", "ALREADY_EXISTS":
		return ReasonConflict
	default:
		return ReasonUnclassified
	}
}

func addErrorHints(result *APIError, message string) {
	message = strings.ToLower(message)
	result.SizeLimitHint = result.SizeLimitHint || strings.Contains(message, "size_limit") || strings.Contains(message, "size limit")
	result.DeleteProtectionHint = result.DeleteProtectionHint || strings.Contains(message, "delete_protection") || strings.Contains(message, "delete protection") || strings.Contains(message, "deletion protection")
	result.PlanOrQuotaHint = result.PlanOrQuotaHint || strings.Contains(message, "quota") || strings.Contains(message, "subscription") || strings.Contains(message, "plan limit") || strings.Contains(message, "storage limit")

	// New hints use whole words and fixed phrases, folding punctuation and case.
	// They describe lexical evidence only, may overlap, and never change Reason.
	words := " " + strings.Join(strings.FieldsFunc(message, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ") + " "
	result.ConstraintHint = result.ConstraintHint || hasHintPhrase(words, "must be", "must not", "required", "requires", "cannot", "invalid", "not valid")
	result.ParsingHint = result.ParsingHint || hasHintPhrase(words, "parse", "parsing", "parser", "malformed", "invalid format", "invalid syntax", "decode", "decoding", "unmarshal", "unmarshalling", "invalid encoding")
	result.TypeHint = result.TypeHint || hasHintPhrase(words, "type", "typed", "integer", "integers", "int", "int32", "int64", "uint", "uint32", "uint64", "number", "numeric", "string", "boolean", "bool", "float", "float32", "float64")
	result.UnitHint = result.UnitHint || hasHintPhrase(words, "unit", "units", "unitless", "suffix", "suffixes", "byte", "bytes", "kb", "kib", "mb", "mib", "gb", "gib", "tb", "tib") || hasNumericUnit(words)
	result.MinimumHint = result.MinimumHint || hasHintPhrase(words, "minimum", "min", "at least", "not less than", "no less than", "not below", "positive", "nonnegative", "non negative", "too small", "too low", "must be greater than", "must be larger than", "must exceed", "cannot be less than", "cannot be smaller than")
	result.MaximumHint = result.MaximumHint || hasHintPhrase(words, "maximum", "max", "at most", "no more than", "not greater than", "not above", "too large", "too high", "must be less than", "must be smaller than", "must not exceed", "cannot exceed", "cannot be greater than", "cannot be larger than")
	result.RangeHint = result.RangeHint || result.MinimumHint || result.MaximumHint || hasHintPhrase(words, "range", "between", "out of bounds", "greater than", "less than", "larger than", "smaller than", "exceed", "exceeds")
	result.BelowCurrentHint = result.BelowCurrentHint || hasHintPhrase(words, "below current", "below the current", "less than current", "less than the current", "smaller than current", "smaller than the current", "lower than current", "lower than the current", "below existing", "below the existing", "less than existing", "less than the existing", "smaller than existing", "smaller than the existing", "below database size", "below the database size", "less than database size", "less than the database size", "smaller than database size", "smaller than the database size", "at least current", "at least the current", "greater than current", "greater than the current", "larger than current", "larger than the current")
	result.UnsupportedHint = result.UnsupportedHint || hasHintPhrase(words, "unsupported", "not supported", "does not support", "unknown field", "unknown setting", "unrecognized field", "unrecognised field", "unexpected field", "invalid field", "not allowed", "cannot set", "cannot be set", "cannot update", "cannot be updated")
	result.ImmutableHint = result.ImmutableHint || hasHintPhrase(words, "immutable", "read only", "readonly", "cannot change", "cannot be changed", "cannot modify", "cannot be modified", "cannot update", "cannot be updated", "may not be changed", "only on creation", "only when creating", "creation only", "only during creation")
	result.AllowanceHint = result.AllowanceHint || hasHintPhrase(words, "allowance", "quota", "subscription", "plan", "plans", "entitlement", "entitled", "storage limit", "storage allowance", "available storage", "remaining storage", "account limit", "organization limit", "organisation limit")
}

func hasHintPhrase(words string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(words, " "+phrase+" ") {
			return true
		}
	}
	return false
}

func hasNumericUnit(words string) bool {
	for _, word := range strings.Fields(words) {
		for i, r := range word {
			if r >= '0' && r <= '9' {
				continue
			}
			if i > 0 {
				switch word[i:] {
				case "b", "kb", "kib", "mb", "mib", "gb", "gib", "tb", "tib":
					return true
				}
			}
			break
		}
	}
	return false
}
