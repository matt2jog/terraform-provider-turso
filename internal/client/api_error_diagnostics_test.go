package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAPIErrorDiagnosticHintsAreLexicalAndPrivate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, message string
		want          APIError
	}{
		{name: "parsing", message: "cannot parse size_limit", want: APIError{ConstraintHint: true, ParsingHint: true}},
		{name: "JSON type", message: "cannot unmarshal string into field size_limit of type int64", want: APIError{ConstraintHint: true, ParsingHint: true, TypeHint: true}},
		{name: "units", message: "size_limit has an invalid unit suffix in bytes", want: APIError{ConstraintHint: true, UnitHint: true}},
		{name: "numeric unit suffix", message: "size_limit must be at least 1mb", want: APIError{ConstraintHint: true, UnitHint: true, RangeHint: true, MinimumHint: true}},
		{name: "range", message: "size_limit is outside the range between bounds", want: APIError{RangeHint: true}},
		{name: "positive minimum", message: "SIZE_LIMIT must\nbe positive", want: APIError{ConstraintHint: true, RangeHint: true, MinimumHint: true}},
		{name: "greater than minimum", message: "size_limit must be greater than the minimum", want: APIError{ConstraintHint: true, RangeHint: true, MinimumHint: true}},
		{name: "not greater than maximum", message: "size_limit cannot be greater than the maximum", want: APIError{ConstraintHint: true, RangeHint: true, MaximumHint: true}},
		{name: "less than maximum", message: "size_limit must be less than the maximum", want: APIError{ConstraintHint: true, RangeHint: true, MaximumHint: true}},
		{name: "below current floor", message: "size_limit cannot be smaller than the current database size", want: APIError{ConstraintHint: true, RangeHint: true, MinimumHint: true, BelowCurrentHint: true}},
		{name: "unsupported", message: "size_limit is not supported", want: APIError{UnsupportedHint: true}},
		{name: "unknown field", message: "unknown field size_limit", want: APIError{UnsupportedHint: true}},
		{name: "immutable", message: "size_limit cannot change after creation", want: APIError{ConstraintHint: true, ImmutableHint: true}},
		{name: "update restriction", message: "size_limit cannot be updated", want: APIError{ConstraintHint: true, UnsupportedHint: true, ImmutableHint: true}},
		{name: "allowance without existing quota terms", message: "size_limit exceeds account allowance", want: APIError{RangeHint: true, AllowanceHint: true}},
		{name: "generic constraint does not imply type or range", message: "size_limit must be provided", want: APIError{ConstraintHint: true}},
		{name: "whole word boundaries", message: "size_limit transition community planning arithmetic", want: APIError{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const private = " private-response-sentinel jane.doe@private.example https://private.example/private-org/private-db top-secret-token"
			body, err := json.Marshal(map[string]any{"error": test.message + private, "ignored": private})
			if err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("X-Private", private)
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(body)
			}))
			err = apiClient.UpdateDatabaseConfiguration(context.Background(), "private-org-sentinel", "private-db-sentinel", UpdateDatabaseConfigurationRequest{SizeLimit: "600000000"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want APIError", err)
			}
			want := test.want
			want.StatusCode = http.StatusBadRequest
			want.Reason = ReasonUnclassified
			want.SizeLimitHint = true
			want.BodyShape = BodyShapeObject
			want.ErrorFieldPresent = true
			if !reflect.DeepEqual(*apiErr, want) {
				t.Fatalf("classification = %#v, want %#v", apiErr, want)
			}
			if attempts.Load() != 1 {
				t.Fatalf("HTTP 400 PATCH attempts = %d, want 1", attempts.Load())
			}
			assertDiagnosticPrivacy(t, apiErr)
		})
	}
}

func TestAPIErrorBodyShapeAndKnownFieldPresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body string
		want       APIError
	}{
		{name: "empty", want: APIError{BodyShape: BodyShapeEmpty}},
		{name: "whitespace", body: " \n\t ", want: APIError{BodyShape: BodyShapeEmpty}},
		{name: "malformed known prefix", body: `{"error":"size_limit must be positive private-response-sentinel`, want: APIError{BodyShape: BodyShapeMalformed}},
		{name: "JSON forbids vertical tab prefix", body: "\v" + `{"code":"INVALID_ARGUMENT","error":"size_limit must be positive"}`, want: APIError{BodyShape: BodyShapeMalformed}},
		{name: "JSON forbids NBSP prefix", body: "\u00a0" + `{"code":"INVALID_ARGUMENT","error":"size_limit must be positive"}`, want: APIError{BodyShape: BodyShapeMalformed}},
		{name: "JSON forbids form feed suffix", body: `{"code":"INVALID_ARGUMENT","error":"size_limit must be positive"}` + "\f", want: APIError{BodyShape: BodyShapeMalformed}},
		{name: "oversized known prefix", body: `{"error":"size_limit must be positive ` + strings.Repeat("x", maxErrorBodyBytes) + `"}`, want: APIError{BodyShape: BodyShapeOversized}},
		{name: "array", body: `[{"error":"size_limit must be positive"}]`, want: APIError{BodyShape: BodyShapeNonObject}},
		{name: "null", body: `null`, want: APIError{BodyShape: BodyShapeNonObject}},
		{name: "string", body: `"size_limit must be positive"`, want: APIError{BodyShape: BodyShapeNonObject}},
		{name: "number", body: `1234`, want: APIError{BodyShape: BodyShapeNonObject}},
		{name: "unknown fields ignored", body: `{"details":"size_limit must be positive private-response-sentinel"}`, want: APIError{BodyShape: BodyShapeObject}},
		{name: "field presence is not validity", body: `{"message":17,"error":false,"code":{}}`, want: APIError{BodyShape: BodyShapeObject, MessageFieldPresent: true, ErrorFieldPresent: true, CodeFieldPresent: true}},
		{name: "unknown code not echoed", body: `{"code":"private-code-sentinel"}`, want: APIError{BodyShape: BodyShapeObject, CodeFieldPresent: true}},
		{name: "empty nested object", body: `{"error":{}}`, want: APIError{BodyShape: BodyShapeObject, ErrorFieldPresent: true, NestedErrorObject: true}},
		{name: "nested known strings", body: `{"error":{"code":"INVALID_ARGUMENT","message":"size_limit must be positive private-response-sentinel"}}`, want: APIError{Reason: ReasonValidation, BodyShape: BodyShapeObject, ErrorFieldPresent: true, CodeFieldPresent: true, MessageFieldPresent: true, NestedErrorObject: true, SizeLimitHint: true, ConstraintHint: true, RangeHint: true, MinimumHint: true}},
		{name: "deeper object ignored", body: `{"error":{"error":{"message":"size_limit must be positive private-response-sentinel"}}}`, want: APIError{BodyShape: BodyShapeObject, ErrorFieldPresent: true, NestedErrorObject: true}},
		{name: "unknown code hints never change reason", body: `{"code":"SIZE_LIMIT_MUST_BE_POSITIVE"}`, want: APIError{BodyShape: BodyShapeObject, CodeFieldPresent: true, SizeLimitHint: true, ConstraintHint: true, RangeHint: true, MinimumHint: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := newAPIError(http.StatusBadRequest, []byte(test.body))
			want := test.want
			want.StatusCode = http.StatusBadRequest
			if want.Reason == "" {
				want.Reason = ReasonUnclassified
			}
			if !reflect.DeepEqual(*got, want) {
				t.Fatalf("classification = %#v, want %#v", got, want)
			}
			assertDiagnosticPrivacy(t, got)
		})
	}
}

func TestAPIErrorDiagnosticsRespectExactBodyBoundary(t *testing.T) {
	t.Parallel()
	const prefix = `{"error":"size_limit must be positive","padding":"`
	const suffix = `"}`
	body := prefix + strings.Repeat("x", maxErrorBodyBytes-len(prefix)-len(suffix)) + suffix
	for _, extra := range []string{"", " "} {
		got := newAPIError(http.StatusBadRequest, []byte(body+extra))
		if extra == "" {
			if got.BodyShape != BodyShapeObject || !got.MinimumHint || !got.ErrorFieldPresent {
				t.Fatalf("body at limit was not classified: %#v", got)
			}
		} else if got.BodyShape != BodyShapeOversized || got.MinimumHint || got.ErrorFieldPresent {
			t.Fatalf("oversized body was inspected: %#v", got)
		}
	}
}

func TestAPIErrorDiagnosticsPreserveCodePrecedence(t *testing.T) {
	t.Parallel()
	err := newAPIError(http.StatusBadRequest, []byte(`{"code":"FORBIDDEN","error":{"code":"INVALID_ARGUMENT","message":"size_limit must be positive"}}`))
	if err.Reason != ReasonAuthorization || !err.MinimumHint || !err.NestedErrorObject {
		t.Fatalf("reason precedence or independent hints changed: %#v", err)
	}
}

func TestAPIErrorDiagnosticsPreservePatchRetryCounts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status, attempts int
	}{
		{status: http.StatusBadRequest, attempts: 1},
		{status: http.StatusTooManyRequests, attempts: defaultAttempts},
		{status: http.StatusServiceUnavailable, attempts: defaultAttempts},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"error":"size_limit must be positive private-response-sentinel"}`)
			}))
			err := apiClient.UpdateDatabaseConfiguration(context.Background(), "private-org-sentinel", "private-db-sentinel", UpdateDatabaseConfigurationRequest{SizeLimit: "600000000"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != test.status || !apiErr.MinimumHint {
				t.Fatalf("final error classification = %#v", err)
			}
			if got := attempts.Load(); got != int32(test.attempts) {
				t.Fatalf("PATCH attempts = %d, want %d", got, test.attempts)
			}
			assertDiagnosticPrivacy(t, apiErr)
		})
	}
}

func TestAPIErrorRenderingUsesOnlyFixedShapeAndHintLabels(t *testing.T) {
	t.Parallel()
	err := &APIError{StatusCode: http.StatusBadRequest, Reason: "private-reason-sentinel", BodyShape: "private-shape-sentinel"}
	text := err.Error()
	const originalPrefix = "Turso API request failed: HTTP 400 (reason=unclassified, size_limit_hint=false, delete_protection_hint=false, plan_or_quota_hint=false)"
	if !strings.HasPrefix(text, originalPrefix+" diagnostics=(") {
		t.Fatalf("original diagnostic tuple was changed: %s", text)
	}
	for _, field := range []string{"size_limit_hint", "delete_protection_hint", "plan_or_quota_hint", "constraint_hint", "parsing_hint", "type_hint", "unit_hint", "range_hint", "minimum_hint", "maximum_hint", "below_current_hint", "unsupported_hint", "immutable_hint", "allowance_hint", "message_field_present", "error_field_present", "code_field_present", "nested_error_object_present"} {
		if strings.Count(text, field+"=false") != 1 {
			t.Fatalf("missing or repeated false label %q", field)
		}
	}
	if !strings.Contains(text, "reason=unclassified") || !strings.Contains(text, "body_shape=unclassified") || strings.Contains(text, "private-") {
		t.Fatalf("unsafe enum rendering: %s", text)
	}
}

func assertDiagnosticPrivacy(t *testing.T, err *APIError) {
	t.Helper()
	for _, rendered := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		for _, forbidden := range []string{"private-", "private.", "jane.doe", "top-secret-token", "https://", "/v1/", "X-Private", "SIZE_LIMIT_MUST_BE_POSITIVE"} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("error leaked private content: %s", rendered)
			}
		}
	}
}
