package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

func TestGroupResourceCRUDAndDeleteProtection(t *testing.T) {
	ctx := context.Background()
	exists := false
	deleteProtection := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/acme/groups":
			exists = true
			_, _ = w.Write([]byte(`{"group":{"name":"main","uuid":"group-id","locations":["aws-us-east-1"],"primary":"us-east-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/groups/main":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(client.GroupResponse{Group: client.Group{Name: "main", UUID: "group-id", Locations: []string{"aws-us-east-1"}, Primary: "us-east-1", DeleteProtection: deleteProtection}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/organizations/acme/groups/main/configuration":
			var body client.GroupConfiguration
			_ = json.NewDecoder(r.Body).Decode(&body)
			deleteProtection = body.DeleteProtection
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/groups/main/configuration":
			_ = json.NewEncoder(w).Encode(client.GroupConfiguration{DeleteProtection: deleteProtection})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/organizations/acme/groups/main":
			exists = false
			w.WriteHeader(http.StatusNoContent)
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
	r := &groupResource{client: apiClient}
	var schemaResponse resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)

	planModel := groupResourceModel{
		Name: types.StringValue("main"), Location: types.StringValue("aws-us-east-1"),
		DeleteProtection: types.BoolValue(true), Locations: types.SetUnknown(types.StringType),
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diagnostics := plan.Set(ctx, &planModel); diagnostics.HasError() {
		t.Fatalf("plan diagnostics: %v", diagnostics)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	var state groupResourceModel
	if diagnostics := createResponse.State.Get(ctx, &state); diagnostics.HasError() {
		t.Fatalf("state diagnostics: %v", diagnostics)
	}
	if state.ID.ValueString() != "acme/main" || !state.DeleteProtection.ValueBool() || !deleteProtection {
		t.Fatalf("state after create = %#v, remote protection=%v", state, deleteProtection)
	}
	if state.Location.ValueString() != "aws-us-east-1" || state.PrimaryLocation.ValueString() != "us-east-1" {
		t.Fatalf("locations after create = location %q primary %q", state.Location.ValueString(), state.PrimaryLocation.ValueString())
	}

	protectedDelete := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: createResponse.State}, &protectedDelete)
	if !protectedDelete.Diagnostics.HasError() || !exists {
		t.Fatalf("protected delete diagnostics=%v exists=%v", protectedDelete.Diagnostics, exists)
	}

	fallbackClient, err := client.New(server.URL, "secret", "fallback", "test", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	r.client = fallbackClient
	state.DeleteProtection = types.BoolValue(false)
	state.Organization = types.StringUnknown()
	updatePlan := tfsdk.Plan{Schema: schemaResponse.Schema}
	_ = updatePlan.Set(ctx, &state)
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: createResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() || deleteProtection {
		t.Fatalf("update diagnostics=%v protection=%v", updateResponse.Diagnostics, deleteProtection)
	}
	deleteResponse := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() || exists {
		t.Fatalf("delete diagnostics=%v exists=%v", deleteResponse.Diagnostics, exists)
	}
}

func TestImportedGroupReadUsesCanonicalLocationKey(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/acme/groups/main" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(client.GroupResponse{Group: client.Group{
			Name:             "main",
			UUID:             "group-id",
			Locations:        []string{"aws-us-east-1"},
			Primary:          "us-east-1",
			DeleteProtection: true,
		}})
	}))
	defer server.Close()

	apiClient, err := client.New(server.URL, "secret", "acme", "test", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	r := &groupResource{client: apiClient}
	var schemaResponse resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)

	importResponse := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	if diagnostics := importResponse.State.Set(ctx, &groupResourceModel{Locations: types.SetNull(types.StringType)}); diagnostics.HasError() {
		t.Fatalf("initialize state diagnostics: %v", diagnostics)
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "acme/main"}, &importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResponse.Diagnostics)
	}

	response := resource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	r.Read(ctx, resource.ReadRequest{State: importResponse.State}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var refreshed groupResourceModel
	if diagnostics := response.State.Get(ctx, &refreshed); diagnostics.HasError() {
		t.Fatalf("state diagnostics: %v", diagnostics)
	}
	if got := refreshed.Location.ValueString(); got != "aws-us-east-1" {
		t.Fatalf("location = %q, want canonical key %q", got, "aws-us-east-1")
	}
	if got := refreshed.PrimaryLocation.ValueString(); got != "us-east-1" {
		t.Fatalf("primary_location = %q, want API region %q", got, "us-east-1")
	}
}

func TestDatabaseResourceCRUDAndDeleteProtection(t *testing.T) {
	ctx := context.Background()
	exists := false
	configuration := client.DatabaseConfiguration{SizeLimit: "500000000"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/acme/databases":
			exists = true
			_, _ = w.Write([]byte(`{"database":{"Name":"career","DbId":"database-id","Hostname":"career-acme.turso.io","group":"main"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/databases/career":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"database":{"Name":"career","DbId":"database-id","Hostname":"career-acme.turso.io","group":"main","regions":["aws-us-east-1"],"primaryRegion":"aws-us-east-1"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/organizations/acme/databases/career/configuration":
			var body client.UpdateDatabaseConfigurationRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			configuration.SizeLimit = body.SizeLimit
			configuration.DeleteProtection = body.DeleteProtection
			_ = json.NewEncoder(w).Encode(configuration)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/acme/databases/career/configuration":
			_ = json.NewEncoder(w).Encode(configuration)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/organizations/acme/databases/career":
			exists = false
			w.WriteHeader(http.StatusNoContent)
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

	planModel := databaseResourceModel{
		Name: types.StringValue("career"), Group: types.StringValue("main"),
		SizeLimitBytes: types.Int64Value(500_000_000), DeleteProtection: types.BoolValue(true),
		Regions: types.SetUnknown(types.StringType),
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diagnostics := plan.Set(ctx, &planModel); diagnostics.HasError() {
		t.Fatalf("plan diagnostics: %v", diagnostics)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	var state databaseResourceModel
	if diagnostics := createResponse.State.Get(ctx, &state); diagnostics.HasError() {
		t.Fatalf("state diagnostics: %v", diagnostics)
	}
	if state.ID.ValueString() != "acme/career" || state.URL.ValueString() != "libsql://career-acme.turso.io" || !state.DeleteProtection.ValueBool() {
		t.Fatalf("state after create = %#v", state)
	}

	protectedDelete := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: createResponse.State}, &protectedDelete)
	if !protectedDelete.Diagnostics.HasError() || !exists {
		t.Fatalf("protected delete diagnostics=%v exists=%v", protectedDelete.Diagnostics, exists)
	}

	fallbackClient, err := client.New(server.URL, "secret", "fallback", "test", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	r.client = fallbackClient
	state.DeleteProtection = types.BoolValue(false)
	state.SizeLimitBytes = types.Int64Value(600_000_000)
	state.Organization = types.StringUnknown()
	updatePlan := tfsdk.Plan{Schema: schemaResponse.Schema}
	_ = updatePlan.Set(ctx, &state)
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: createResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() || configuration.DeleteProtection || configuration.SizeLimit != "600000000" {
		t.Fatalf("update diagnostics=%v configuration=%#v", updateResponse.Diagnostics, configuration)
	}
	deleteResponse := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() || exists {
		t.Fatalf("delete diagnostics=%v exists=%v", deleteResponse.Diagnostics, exists)
	}
}

func TestImportStateSetsOrganizationAndName(t *testing.T) {
	ctx := context.Background()
	for name, r := range map[string]resource.ResourceWithImportState{
		"group":    &groupResource{},
		"database": &databaseResource{},
	} {
		t.Run(name, func(t *testing.T) {
			var schemaResponse resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
			response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			if name == "group" {
				response.Diagnostics.Append(response.State.Set(ctx, &groupResourceModel{Locations: types.SetNull(types.StringType)})...)
			} else {
				response.Diagnostics.Append(response.State.Set(ctx, &databaseResourceModel{Regions: types.SetNull(types.StringType)})...)
			}
			if response.Diagnostics.HasError() {
				t.Fatalf("initialize state diagnostics: %v", response.Diagnostics)
			}
			r.ImportState(ctx, resource.ImportStateRequest{ID: "acme/career"}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("import diagnostics: %v", response.Diagnostics)
			}
			var organization, objectName types.String
			response.Diagnostics.Append(response.State.GetAttribute(ctx, path.Root("organization"), &organization)...)
			response.Diagnostics.Append(response.State.GetAttribute(ctx, path.Root("name"), &objectName)...)
			if response.Diagnostics.HasError() || organization.ValueString() != "acme" || objectName.ValueString() != "career" {
				t.Fatalf("import state org=%q name=%q diagnostics=%v", organization.ValueString(), objectName.ValueString(), response.Diagnostics)
			}
		})
	}
}
