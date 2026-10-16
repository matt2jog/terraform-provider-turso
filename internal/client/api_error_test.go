package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAPIErrorsClassifyOnlyBoundedKnownJSONShapes(t *testing.T) {
	t.Parallel()
	const private = "private-response-sentinel top-secret-token https://private.example/private-org/private-db"
	for _, test := range []struct {
		name           string
		body           string
		reason         ErrorReason
		sizeHint       bool
		protectionHint bool
		planHint       bool
	}{
		{name: "empty", reason: ReasonUnclassified},
		{name: "malformed", body: `{"code":"INVALID_ARGUMENT","message":"size_limit ` + private, reason: ReasonUnclassified},
		{name: "oversized valid JSON", body: `{"code":"INVALID_ARGUMENT","message":"size_limit ` + private + strings.Repeat("x", maxErrorBodyBytes) + `"}`, reason: ReasonUnclassified},
		{name: "plain text", body: "size_limit " + private, reason: ReasonUnclassified},
		{name: "array", body: `[{"code":"INVALID_ARGUMENT","message":"size_limit ` + private + `"}]`, reason: ReasonUnclassified},
		{name: "unknown fields", body: `{"details":"size_limit ` + private + `"}`, reason: ReasonUnclassified},
		{name: "unknown code", body: `{"code":"private-code-sentinel","message":"` + private + `"}`, reason: ReasonUnclassified},
		{name: "message hint without assumed cause", body: `{"message":"size_limit ` + private + `"}`, reason: ReasonUnclassified, sizeHint: true},
		{name: "string error hints", body: `{"error":"deletion protection and subscription ` + private + `"}`, reason: ReasonUnclassified, protectionHint: true, planHint: true},
		{name: "top-level code", body: `{"code":"INVALID_ARGUMENT","message":"size limit ` + private + `"}`, reason: ReasonValidation, sizeHint: true},
		{name: "nested code and hints", body: `{"error":{"code":"PLAN_LIMIT_EXCEEDED","message":"delete_protection quota ` + private + `"}}`, reason: ReasonQuotaOrPlan, protectionHint: true, planHint: true},
		{name: "authentication code", body: `{"code":"UNAUTHENTICATED","message":"` + private + `"}`, reason: ReasonAuthentication},
		{name: "authorization code", body: `{"code":"PERMISSION_DENIED","message":"` + private + `"}`, reason: ReasonAuthorization},
		{name: "rate limit code", body: `{"code":"TOO_MANY_REQUESTS","message":"` + private + `"}`, reason: ReasonRateLimit},
		{name: "conflict code", body: `{"code":"CONFLICT","message":"` + private + `"}`, reason: ReasonConflict},
		{name: "deeper objects ignored", body: `{"error":{"error":{"code":"INVALID_ARGUMENT","message":"size_limit ` + private + `"}}}`, reason: ReasonUnclassified},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("X-Private-Sentinel", private)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, test.body)
			}))
			err := apiClient.UpdateDatabaseConfiguration(context.Background(), "private-org-sentinel", "private-db-sentinel", UpdateDatabaseConfigurationRequest{SizeLimit: "600000000"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("error = %T %v, want HTTP 400 APIError", err, err)
			}
			if apiErr.Reason != test.reason || apiErr.SizeLimitHint != test.sizeHint || apiErr.DeleteProtectionHint != test.protectionHint || apiErr.PlanOrQuotaHint != test.planHint {
				t.Fatalf("unexpected error classification: %#v", apiErr)
			}
			if attempts.Load() != 1 {
				t.Fatalf("HTTP 400 PATCH attempts = %d, want 1", attempts.Load())
			}
			for _, rendered := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", apiErr)} {
				for _, forbidden := range []string{"private-", "top-secret-token", "https://", "/v1/", "X-Private-Sentinel"} {
					if strings.Contains(rendered, forbidden) {
						t.Fatalf("error leaked private content: %s", rendered)
					}
				}
			}
		})
	}
}

type countedErrorBody struct {
	read   int
	closed bool
}

func (b *countedErrorBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}

func (b *countedErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestAPIErrorBodyReadIsBoundedAndClosed(t *testing.T) {
	t.Parallel()
	body := &countedErrorBody{}
	apiClient, err := New("https://api.turso.tech", "secret", "acme", "test", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: body, Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = apiClient.ListLocations(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Reason != ReasonUnclassified {
		t.Fatalf("error = %v, want unclassified API error", err)
	}
	if body.read != maxErrorBodyBytes+1 || !body.closed {
		t.Fatalf("body read = %d, closed = %t", body.read, body.closed)
	}
}

func TestAPIErrorRenderingRejectsUnrecognizedReason(t *testing.T) {
	t.Parallel()
	err := &APIError{StatusCode: http.StatusBadRequest, Reason: "private-reason-sentinel"}
	if strings.Contains(err.Error(), "private-reason") || !strings.Contains(err.Error(), "reason=unclassified") {
		t.Fatalf("unsafe error reason: %s", err.Error())
	}
}

type failedResponseBody struct {
	readErr, closeErr error
}

func (b failedResponseBody) Read([]byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return 0, io.EOF
}

func (b failedResponseBody) Close() error { return b.closeErr }

func TestRequestFailuresPreserveOnlySafeContextClassification(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"transport", "read", "close"} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			t.Run(stage+"/"+cause.Error(), func(t *testing.T) {
				t.Parallel()
				privateErr := fmt.Errorf("private-error-sentinel https://private.example/private-org/private-db top-secret-token: %w", cause)
				apiClient, err := New("https://api.turso.tech", "top-secret-token", "private-org", "test", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					if stage == "transport" {
						return nil, privateErr
					}
					body := failedResponseBody{}
					if stage == "read" {
						body.readErr = privateErr
					} else {
						body.closeErr = privateErr
					}
					return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
				})})
				if err != nil {
					t.Fatal(err)
				}
				apiClient.maxAttempts = 1
				_, err = apiClient.ListLocations(context.Background())
				if !errors.Is(err, cause) {
					t.Fatalf("error = %v, want safe classification %v", err, cause)
				}
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "top-secret") || strings.Contains(err.Error(), "https://") {
					t.Fatalf("error leaked private content: %v", err)
				}
			})
		}
	}
}
