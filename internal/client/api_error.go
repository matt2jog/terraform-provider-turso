package client

import (
	"encoding/json"
	"strings"
)

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
	result := &APIError{StatusCode: status, Reason: ReasonUnclassified}
	if len(body) == 0 || len(body) > maxErrorBodyBytes {
		return result
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return result
	}
	inspectErrorObject(result, object, true)
	return result
}

// Only known message/error/code shapes are inspected, and no response strings
// are retained. Unknown fields, codes, malformed JSON and oversized bodies are
// deliberately unclassified. Nested objects are limited to the error member.
func inspectErrorObject(result *APIError, object map[string]json.RawMessage, nested bool) {
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
}
