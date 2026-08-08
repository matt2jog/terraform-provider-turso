package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

func TestProviderSurface(t *testing.T) {
	t.Parallel()
	p := New("test")().(*tursoProvider)
	var metadata frameworkprovider.MetadataResponse
	p.Metadata(context.Background(), frameworkprovider.MetadataRequest{}, &metadata)
	if metadata.TypeName != "turso" || metadata.Version != "test" {
		t.Fatalf("metadata = %#v", metadata)
	}
	var schemaResponse frameworkprovider.SchemaResponse
	p.Schema(context.Background(), frameworkprovider.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics = %v", schemaResponse.Diagnostics)
	}
	if got := len(schemaResponse.Schema.Attributes); got != 3 {
		t.Fatalf("provider attributes = %d, want 3", got)
	}
	if got := len(p.Resources(context.Background())); got != 2 {
		t.Fatalf("resources = %d, want 2", got)
	}
	if got := len(p.DataSources(context.Background())); got != 4 {
		t.Fatalf("data sources = %d, want 4", got)
	}
}

func TestProtocol6Schema(t *testing.T) {
	t.Parallel()
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("protocol server: %v", err)
	}
	response, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema() error = %v", err)
	}
	if len(response.Diagnostics) != 0 {
		t.Fatalf("schema diagnostics = %#v", response.Diagnostics)
	}
	for _, name := range []string{"turso_group", "turso_database"} {
		if response.ResourceSchemas[name] == nil {
			t.Errorf("resource schema %q missing", name)
		}
	}
	for _, name := range []string{"turso_locations", "turso_organization", "turso_group", "turso_database"} {
		if response.DataSourceSchemas[name] == nil {
			t.Errorf("data source schema %q missing", name)
		}
	}
}

func TestParseImportID(t *testing.T) {
	t.Parallel()
	org, name, err := parseImportID("matt2jog/career-prod")
	if err != nil || org != "matt2jog" || name != "career-prod" {
		t.Fatalf("parseImportID() = %q, %q, %v", org, name, err)
	}
	for _, invalid := range []string{"career-prod", "a/b/c", "UPPER/name", "org/name_underscore", "/name"} {
		if _, _, err := parseImportID(invalid); err == nil {
			t.Errorf("parseImportID(%q) error = nil", invalid)
		}
	}
}

func TestParseSizeLimit(t *testing.T) {
	t.Parallel()
	tests := map[string]int64{
		"500000000": 500_000_000,
		"500mb":     500_000_000,
		"2 GiB":     2 << 30,
		"1kb":       1_000,
		"":          0,
	}
	for input, want := range tests {
		got, err := parseSizeLimit(input)
		if err != nil || got != want {
			t.Errorf("parseSizeLimit(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	if _, err := parseSizeLimit("lots"); err == nil {
		t.Fatal("parseSizeLimit(lots) error = nil")
	}
}

func TestConfiguredStringPrecedence(t *testing.T) {
	t.Setenv("TURSO_ORG", "from-env")
	if got := configuredString(types.StringValue("from-config"), "TURSO_ORG"); got != "from-config" {
		t.Fatalf("configured value = %q", got)
	}
	if got := configuredString(types.StringNull(), "TURSO_ORG"); got != "from-env" {
		t.Fatalf("environment value = %q", got)
	}
}

func TestStateProjection(t *testing.T) {
	t.Parallel()
	database := &client.Database{
		Name: "career", UUID: "db-id", Hostname: "career-acme.turso.io", Group: "main",
		Regions: []string{"aws-us-east-1"}, PrimaryRegion: "aws-us-east-1",
	}
	configuration := &client.DatabaseConfiguration{SizeLimit: "500mb", DeleteProtection: true}
	var state databaseResourceModel
	var diagnostics diag.Diagnostics
	setDatabaseResourceState(context.Background(), &state, "acme", database, configuration, &diagnostics)
	if diagnostics.HasError() {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if state.ID.ValueString() != "acme/career" || state.URL.ValueString() != "libsql://career-acme.turso.io" || state.SizeLimitBytes.ValueInt64() != 500_000_000 || !state.DeleteProtection.ValueBool() {
		t.Fatalf("state = %#v", state)
	}
}

func TestResourceSchemas(t *testing.T) {
	t.Parallel()
	for name, factory := range map[string]func() resource.Resource{"group": NewGroupResource, "database": NewDatabaseResource} {
		t.Run(name, func(t *testing.T) {
			var response resource.SchemaResponse
			factory().Schema(context.Background(), resource.SchemaRequest{}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics = %v", response.Diagnostics)
			}
			if response.Schema.Attributes["delete_protection"] == nil {
				t.Fatal("delete_protection missing")
			}
		})
	}
}
