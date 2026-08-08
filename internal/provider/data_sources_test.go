package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

func TestDataSourceReads(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/locations":
			_, _ = w.Write([]byte(`{"locations":{"aws-us-east-1":"AWS US East"}}`))
		case "/v1/organizations":
			_, _ = w.Write([]byte(`[{"name":"Personal","slug":"acme","type":"personal","plan_id":"developer","plan_timeline":"monthly","overages":false,"require_mfa":true,"blocked_reads":false,"blocked_writes":false,"platform":""}]`))
		case "/v1/organizations/acme/groups/main":
			_, _ = w.Write([]byte(`{"group":{"name":"main","uuid":"group-id","locations":["aws-us-east-1"],"primary":"aws-us-east-1","delete_protection":true}}`))
		case "/v1/organizations/acme/databases/career":
			_, _ = w.Write([]byte(`{"database":{"Name":"career","DbId":"database-id","Hostname":"career-acme.turso.io","group":"main","regions":["aws-us-east-1"],"primaryRegion":"aws-us-east-1","delete_protection":true}}`))
		case "/v1/organizations/acme/databases/career/configuration":
			_, _ = w.Write([]byte(`{"size_limit":"500mb","delete_protection":true,"block_reads":false,"block_writes":false}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	apiClient, err := client.New(server.URL, "fake-token", "acme", "test", server.Client())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("locations", func(t *testing.T) {
		d := &locationsDataSource{client: apiClient}
		var schemaResponse datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
		response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
		d.Read(context.Background(), datasource.ReadRequest{}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", response.Diagnostics)
		}
		var state locationsDataSourceModel
		response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
		if response.Diagnostics.HasError() || state.ID.ValueString() != "locations" || len(state.Locations.Elements()) != 1 {
			t.Fatalf("state=%#v diagnostics=%v", state, response.Diagnostics)
		}
	})

	t.Run("organization", func(t *testing.T) {
		d := &organizationDataSource{client: apiClient}
		var schemaResponse datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
		plan := tfsdk.Plan{Schema: schemaResponse.Schema}
		if diagnostics := plan.Set(context.Background(), &organizationDataSourceModel{Slug: types.StringValue("acme")}); diagnostics.HasError() {
			t.Fatalf("config diagnostics: %v", diagnostics)
		}
		config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw}
		response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
		d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", response.Diagnostics)
		}
		var state organizationDataSourceModel
		response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
		if response.Diagnostics.HasError() || state.ID.ValueString() != "acme" || state.PlanID.ValueString() != "developer" || !state.RequireMFA.ValueBool() {
			t.Fatalf("state=%#v diagnostics=%v", state, response.Diagnostics)
		}
	})

	t.Run("group", func(t *testing.T) {
		d := &groupDataSource{client: apiClient}
		var schemaResponse datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
		model := groupDataSourceModel{Organization: types.StringValue("acme"), Name: types.StringValue("main"), Locations: types.SetNull(types.StringType)}
		plan := tfsdk.Plan{Schema: schemaResponse.Schema}
		if diagnostics := plan.Set(context.Background(), &model); diagnostics.HasError() {
			t.Fatalf("config diagnostics: %v", diagnostics)
		}
		config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw}
		response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
		d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", response.Diagnostics)
		}
		var state groupDataSourceModel
		response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
		if response.Diagnostics.HasError() || state.ID.ValueString() != "acme/main" || !state.DeleteProtection.ValueBool() {
			t.Fatalf("state=%#v diagnostics=%v", state, response.Diagnostics)
		}
	})

	t.Run("database", func(t *testing.T) {
		d := &databaseDataSource{client: apiClient}
		var schemaResponse datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
		model := databaseDataSourceModel{Organization: types.StringValue("acme"), Name: types.StringValue("career"), Regions: types.SetNull(types.StringType)}
		plan := tfsdk.Plan{Schema: schemaResponse.Schema}
		if diagnostics := plan.Set(context.Background(), &model); diagnostics.HasError() {
			t.Fatalf("config diagnostics: %v", diagnostics)
		}
		config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw}
		response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
		d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", response.Diagnostics)
		}
		var state databaseDataSourceModel
		response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
		if response.Diagnostics.HasError() || state.ID.ValueString() != "acme/career" || state.SizeLimitBytes.ValueInt64() != 500_000_000 || state.URL.ValueString() != "libsql://career-acme.turso.io" {
			t.Fatalf("state=%#v diagnostics=%v", state, response.Diagnostics)
		}
	})
}
