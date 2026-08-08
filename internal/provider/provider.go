package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

var _ provider.Provider = &tursoProvider{}

type tursoProvider struct {
	version string
}

type tursoProviderModel struct {
	Organization types.String `tfsdk:"organization"`
	Token        types.String `tfsdk:"token"`
	APIURL       types.String `tfsdk:"api_url"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &tursoProvider{version: version} }
}

func (p *tursoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "turso"
	resp.Version = p.version
}

func (p *tursoProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages stable Turso Platform API infrastructure. Database and group auth tokens are intentionally outside this provider so secret values never enter Terraform state.",
		Attributes: map[string]schema.Attribute{
			"organization": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Turso organization slug. Defaults to TURSO_ORGANIZATION, then TURSO_ORG.",
			},
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Turso Platform API token. Defaults to TURSO_API_TOKEN, then TURSO_API_KEY.",
			},
			"api_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Turso Platform API base URL. Defaults to TURSO_API_URL or https://api.turso.tech.",
			},
		},
	}
}

func (p *tursoProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config tursoProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Organization.IsUnknown() || config.Token.IsUnknown() || config.APIURL.IsUnknown() {
		return
	}

	organization := configuredString(config.Organization, "TURSO_ORGANIZATION", "TURSO_ORG")
	token := configuredString(config.Token, "TURSO_API_TOKEN", "TURSO_API_KEY")
	apiURL := configuredString(config.APIURL, "TURSO_API_URL")
	if apiURL == "" {
		apiURL = "https://api.turso.tech"
	}
	if organization == "" {
		resp.Diagnostics.AddError("Missing Turso organization", "Set provider.organization, TURSO_ORGANIZATION, or TURSO_ORG.")
	} else if !objectNamePattern.MatchString(organization) {
		resp.Diagnostics.AddError("Invalid Turso organization", "The organization slug must contain lowercase letters, digits, and dashes and be at most 64 characters.")
	}
	if token == "" {
		resp.Diagnostics.AddError("Missing Turso API token", "Set provider.token, TURSO_API_TOKEN, or TURSO_API_KEY. The token is used only in memory.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiClient, err := client.New(apiURL, token, organization, p.version, nil)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Turso provider configuration", err.Error())
		return
	}
	resp.ResourceData = apiClient
	resp.DataSourceData = apiClient
}

func (p *tursoProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewGroupResource, NewDatabaseResource}
}

func (p *tursoProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewLocationsDataSource,
		NewOrganizationDataSource,
		NewGroupDataSource,
		NewDatabaseDataSource,
	}
}

func configuredString(value types.String, envNames ...string) string {
	if !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) != "" {
		return strings.TrimSpace(value.ValueString())
	}
	for _, name := range envNames {
		if candidate := strings.TrimSpace(os.Getenv(name)); candidate != "" {
			return candidate
		}
	}
	return ""
}

func configureClient(providerData any, destination **client.Client) error {
	if providerData == nil {
		return nil
	}
	apiClient, ok := providerData.(*client.Client)
	if !ok {
		return fmt.Errorf("expected *client.Client, got %T", providerData)
	}
	*destination = apiClient
	return nil
}
