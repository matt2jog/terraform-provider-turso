package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

func TestDatabaseUpdatePatchesOnlyChangedSettings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		oldSize, newSize      int64
		oldProtection         bool
		newProtection         bool
		wantPatch             map[string]any
		firstReadWrongSize    bool
		firstReadWrongProtect bool
		unknownPlannedSize    bool
		unknownPlannedProtect bool
	}{
		{
			name: "size only confirms unchanged protection", oldSize: 500_000_000, newSize: 600_000_000,
			oldProtection: true, newProtection: true, wantPatch: map[string]any{"size_limit": "600000000"}, firstReadWrongProtect: true,
		},
		{
			name: "protection only preserves explicit false and confirms unchanged size", oldSize: 500_000_000, newSize: 500_000_000,
			oldProtection: true, newProtection: false, wantPatch: map[string]any{"delete_protection": false}, firstReadWrongSize: true,
		},
		{
			name: "protection only enables protection", oldSize: 500_000_000, newSize: 500_000_000,
			oldProtection: false, newProtection: true, wantPatch: map[string]any{"delete_protection": true},
		},
		{
			name: "both settings", oldSize: 500_000_000, newSize: 600_000_000,
			oldProtection: true, newProtection: false, wantPatch: map[string]any{"size_limit": "600000000", "delete_protection": false},
		},
		{
			name: "omitted computed size retains prior target", oldSize: 500_000_000, newSize: 500_000_000,
			oldProtection: true, newProtection: false, wantPatch: map[string]any{"delete_protection": false}, unknownPlannedSize: true, firstReadWrongSize: true,
		},
		{
			name: "unknown computed protection retains prior target", oldSize: 500_000_000, newSize: 600_000_000,
			oldProtection: true, newProtection: true, wantPatch: map[string]any{"size_limit": "600000000"}, unknownPlannedProtect: true, firstReadWrongProtect: true,
		},
		{
			name: "unchanged settings use read-only confirmation", oldSize: 500_000_000, newSize: 500_000_000,
			oldProtection: true, newProtection: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var patches, configReads atomic.Int32
			configuration := client.DatabaseConfiguration{SizeLimit: strconv.FormatInt(test.oldSize, 10), DeleteProtection: test.oldProtection}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/organizations/acme/databases/app/configuration":
					patches.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode PATCH: %v", err)
					}
					if !reflect.DeepEqual(body, test.wantPatch) {
						t.Errorf("PATCH = %#v, want %#v", body, test.wantPatch)
					}
					if size, ok := body["size_limit"].(string); ok {
						configuration.SizeLimit = size
					}
					if protection, ok := body["delete_protection"].(bool); ok {
						configuration.DeleteProtection = protection
					}
					// The PATCH response alone must never confirm the update.
					_ = json.NewEncoder(w).Encode(configuration)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/databases/app":
					_ = json.NewEncoder(w).Encode(client.DatabaseResponse{Database: client.Database{Name: "app", Group: "main", Hostname: "app.example", Regions: []string{"aws-us-east-1"}}})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/databases/app/configuration":
					response := configuration
					if configReads.Add(1) == 1 {
						if test.firstReadWrongSize {
							response.SizeLimit = "700000000"
						}
						if test.firstReadWrongProtect {
							response.DeleteProtection = !test.newProtection
						}
					}
					_ = json.NewEncoder(w).Encode(response)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			apiClient, err := client.New(server.URL, "secret", "acme", "test", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			r := &databaseResource{client: apiClient}
			var schemaResponse resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
			model := databaseResourceModel{
				Organization: types.StringValue("acme"), Name: types.StringValue("app"), Group: types.StringValue("main"),
				SizeLimitBytes: types.Int64Value(test.oldSize), DeleteProtection: types.BoolValue(test.oldProtection), Regions: types.SetUnknown(types.StringType),
			}
			state := tfsdk.State{Schema: schemaResponse.Schema}
			if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
				t.Fatalf("state diagnostics: %v", diagnostics)
			}
			model.SizeLimitBytes = types.Int64Value(test.newSize)
			model.DeleteProtection = types.BoolValue(test.newProtection)
			if test.unknownPlannedSize {
				model.SizeLimitBytes = types.Int64Unknown()
			}
			if test.unknownPlannedProtect {
				model.DeleteProtection = types.BoolUnknown()
			}
			plan := tfsdk.Plan{Schema: schemaResponse.Schema}
			if diagnostics := plan.Set(ctx, &model); diagnostics.HasError() {
				t.Fatalf("plan diagnostics: %v", diagnostics)
			}
			response := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("update diagnostics: %v", response.Diagnostics)
			}
			var refreshed databaseResourceModel
			if diagnostics := response.State.Get(ctx, &refreshed); diagnostics.HasError() {
				t.Fatalf("refreshed state diagnostics: %v", diagnostics)
			}
			if refreshed.SizeLimitBytes.ValueInt64() != test.newSize || refreshed.DeleteProtection.ValueBool() != test.newProtection {
				t.Fatalf("refreshed configuration = %v / %v", refreshed.SizeLimitBytes, refreshed.DeleteProtection)
			}
			wantPatches := int32(0)
			if test.wantPatch != nil {
				wantPatches = 1
			}
			if patches.Load() != wantPatches {
				t.Fatalf("PATCH count = %d, want %d", patches.Load(), wantPatches)
			}
			wantReads := int32(1)
			if test.firstReadWrongSize || test.firstReadWrongProtect {
				wantReads = 2
			}
			if configReads.Load() != wantReads {
				t.Fatalf("configuration GET count = %d, want %d", configReads.Load(), wantReads)
			}
		})
	}
}

func TestDatabaseUpdateRejectsUnverifiableTargetsBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		size       types.Int64
		protection types.Bool
	}{
		{name: "unknown size", size: types.Int64Unknown(), protection: types.BoolValue(true)},
		{name: "null size", size: types.Int64Null(), protection: types.BoolValue(true)},
		{name: "unknown protection", size: types.Int64Value(500_000_000), protection: types.BoolUnknown()},
		{name: "null protection", size: types.Int64Value(500_000_000), protection: types.BoolNull()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			apiClient, err := client.New(server.URL, "secret", "acme", "test", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			r := &databaseResource{client: apiClient}
			var schemaResponse resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
			model := databaseResourceModel{
				Organization: types.StringValue("acme"), Name: types.StringValue("app"), Group: types.StringValue("main"),
				SizeLimitBytes: test.size, DeleteProtection: test.protection, Regions: types.SetUnknown(types.StringType),
			}
			state := tfsdk.State{Schema: schemaResponse.Schema}
			if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
				t.Fatalf("state diagnostics: %v", diagnostics)
			}
			plan := tfsdk.Plan{Schema: schemaResponse.Schema}
			if diagnostics := plan.Set(ctx, &model); diagnostics.HasError() {
				t.Fatalf("plan diagnostics: %v", diagnostics)
			}
			response := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
			if !response.Diagnostics.HasError() || requests.Load() != 0 {
				t.Fatalf("diagnostics = %v, HTTP request count = %d", response.Diagnostics, requests.Load())
			}
		})
	}
}
