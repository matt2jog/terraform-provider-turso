package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	apiClient, err := New(server.URL, "top-secret-token", "acme", "test", server.Client())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiClient.baseDelay = time.Millisecond
	return apiClient
}

func TestNewValidatesConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, url, token, org string
		wantError             bool
	}{
		{name: "valid production", url: "https://api.turso.tech", token: "x", org: "acme"},
		{name: "valid local test", url: "http://127.0.0.1:8080", token: "x", org: "acme"},
		{name: "missing token", url: "https://api.turso.tech", org: "acme", wantError: true},
		{name: "missing organization", url: "https://api.turso.tech", token: "x", wantError: true},
		{name: "external http rejected", url: "http://example.com", token: "x", org: "acme", wantError: true},
		{name: "URL user info rejected", url: "https://user@api.turso.tech", token: "x", org: "acme", wantError: true},
		{name: "invalid URL", url: "://bad", token: "x", org: "acme", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.url, test.token, test.org, "test", nil)
			if (err != nil) != test.wantError {
				t.Fatalf("New() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestRequestHeadersAndRedactedAPIError(t *testing.T) {
	t.Parallel()
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer top-secret-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "terraform-provider-turso/test" {
			t.Errorf("User-Agent = %q", got)
		}
		http.Error(w, `top-secret-token should never be surfaced`, http.StatusBadRequest)
	}))

	_, err := apiClient.ListLocations(context.Background())
	if err == nil {
		t.Fatal("ListLocations() error = nil")
	}
	if strings.Contains(err.Error(), "top-secret-token") || strings.Contains(err.Error(), "should never") {
		t.Fatalf("error leaked response or credential: %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestTransportErrorRedactsToken(t *testing.T) {
	t.Parallel()
	const token = "transport-secret-token"
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upstream accidentally repeated " + token)
	})}
	apiClient, err := New("https://api.turso.tech", token, "acme", "test", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	apiClient.maxAttempts = 1
	_, err = apiClient.ListLocations(context.Background())
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("transport error was not safely redacted: %v", err)
	}
}

func TestGetRetriesRetryableResponses(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"locations":{"aws-us-east-1":"AWS US East"}}`))
		}
	}))

	locations, err := apiClient.ListLocations(context.Background())
	if err != nil {
		t.Fatalf("ListLocations() error = %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if locations["aws-us-east-1"] != "AWS US East" {
		t.Fatalf("locations = %#v", locations)
	}
}

func TestPostIsNotRetried(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, err := apiClient.CreateGroup(context.Background(), "acme", "main", "aws-us-east-1")
	if err == nil {
		t.Fatal("CreateGroup() error = nil")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestRetryHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	apiClient.baseDelay = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := apiClient.ListLocations(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestGroupCRUDAndReadiness(t *testing.T) {
	t.Parallel()
	var exists bool
	var protected bool
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/acme/groups":
			var body CreateGroupRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body.Name != "main" || body.Location != "aws-us-east-1" {
				t.Errorf("create body = %#v", body)
			}
			exists = true
			_, _ = w.Write([]byte(`{"group":{"name":"main","uuid":"group-uuid","locations":["aws-us-east-1"],"primary":"aws-us-east-1","delete_protection":false}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/groups/main":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(GroupResponse{Group: Group{Name: "main", UUID: "group-uuid", Locations: []string{"aws-us-east-1"}, Primary: "aws-us-east-1", DeleteProtection: protected}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/organizations/acme/groups/main/configuration":
			var body GroupConfiguration
			_ = json.NewDecoder(r.Body).Decode(&body)
			protected = body.DeleteProtection
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/groups/main/configuration":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(GroupConfiguration{DeleteProtection: protected})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/organizations/acme/groups/main":
			exists = false
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	if _, err := apiClient.CreateGroup(context.Background(), "acme", "main", "aws-us-east-1"); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if err := apiClient.UpdateGroupConfiguration(context.Background(), "acme", "main", true); err != nil {
		t.Fatalf("UpdateGroupConfiguration() error = %v", err)
	}
	group, err := apiClient.WaitForGroup(context.Background(), "acme", "main", true)
	if err != nil || group.UUID != "group-uuid" || !group.DeleteProtection {
		t.Fatalf("WaitForGroup() = %#v, %v", group, err)
	}
	if err := apiClient.DeleteGroup(context.Background(), "acme", "main"); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	if _, err := apiClient.WaitForGroup(context.Background(), "acme", "main", false); err != nil {
		t.Fatalf("wait for deletion error = %v", err)
	}
}

func TestDatabaseCRUDAndConfiguration(t *testing.T) {
	t.Parallel()
	var exists bool
	configuration := DatabaseConfiguration{SizeLimit: "500000000", DeleteProtection: false}
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/acme/databases":
			var body CreateDatabaseRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "career" || body.Group != "main" || body.SizeLimit != "500000000" {
				t.Errorf("create body = %#v", body)
			}
			exists = true
			_, _ = w.Write([]byte(`{"database":{"Name":"career","DbId":"db-uuid","Hostname":"career-acme.turso.io"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/databases/career":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"database":{"Name":"career","DbId":"db-uuid","Hostname":"career-acme.turso.io","group":"main","regions":["aws-us-east-1"],"primaryRegion":"aws-us-east-1"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/organizations/acme/databases/career/configuration":
			var body UpdateDatabaseConfigurationRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			configuration.SizeLimit = body.SizeLimit
			configuration.DeleteProtection = body.DeleteProtection
			_ = json.NewEncoder(w).Encode(configuration)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/databases/career/configuration":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(configuration)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/organizations/acme/databases/career":
			exists = false
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	if _, err := apiClient.CreateDatabase(context.Background(), "acme", "career", "main", "500000000"); err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	if err := apiClient.UpdateDatabaseConfiguration(context.Background(), "acme", "career", "600000000", true); err != nil {
		t.Fatalf("UpdateDatabaseConfiguration() error = %v", err)
	}
	database, config, err := apiClient.WaitForDatabase(context.Background(), "acme", "career", true)
	if err != nil || database.UUID != "db-uuid" || config.SizeLimit != "600000000" || !config.DeleteProtection {
		t.Fatalf("WaitForDatabase() = %#v, %#v, %v", database, config, err)
	}
	if err := apiClient.DeleteDatabase(context.Background(), "acme", "career"); err != nil {
		t.Fatalf("DeleteDatabase() error = %v", err)
	}
	if _, _, err := apiClient.WaitForDatabase(context.Background(), "acme", "career", false); err != nil {
		t.Fatalf("wait for deletion error = %v", err)
	}
}

func TestNotFoundUsesSentinel(t *testing.T) {
	t.Parallel()
	apiClient := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := apiClient.ListLocations(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
