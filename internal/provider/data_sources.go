package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

var _ datasource.DataSource = &locationsDataSource{}
var _ datasource.DataSourceWithConfigure = &locationsDataSource{}
var _ datasource.DataSource = &organizationDataSource{}
var _ datasource.DataSourceWithConfigure = &organizationDataSource{}
var _ datasource.DataSource = &groupDataSource{}
var _ datasource.DataSourceWithConfigure = &groupDataSource{}
var _ datasource.DataSource = &databaseDataSource{}
var _ datasource.DataSourceWithConfigure = &databaseDataSource{}

type locationsDataSource struct{ client *client.Client }
type organizationDataSource struct{ client *client.Client }
type groupDataSource struct{ client *client.Client }
type databaseDataSource struct{ client *client.Client }

type locationsDataSourceModel struct {
	ID        types.String `tfsdk:"id"`
	Locations types.Map    `tfsdk:"locations"`
}

type organizationDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	Slug          types.String `tfsdk:"slug"`
	Name          types.String `tfsdk:"name"`
	Type          types.String `tfsdk:"type"`
	PlanID        types.String `tfsdk:"plan_id"`
	PlanTimeline  types.String `tfsdk:"plan_timeline"`
	Overages      types.Bool   `tfsdk:"overages"`
	RequireMFA    types.Bool   `tfsdk:"require_mfa"`
	BlockedReads  types.Bool   `tfsdk:"blocked_reads"`
	BlockedWrites types.Bool   `tfsdk:"blocked_writes"`
	Platform      types.String `tfsdk:"platform"`
}

type groupDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Organization     types.String `tfsdk:"organization"`
	Name             types.String `tfsdk:"name"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	UUID             types.String `tfsdk:"uuid"`
	PrimaryLocation  types.String `tfsdk:"primary_location"`
	Locations        types.Set    `tfsdk:"locations"`
}

type databaseDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Organization     types.String `tfsdk:"organization"`
	Name             types.String `tfsdk:"name"`
	Group            types.String `tfsdk:"group"`
	SizeLimitBytes   types.Int64  `tfsdk:"size_limit_bytes"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	UUID             types.String `tfsdk:"uuid"`
	Hostname         types.String `tfsdk:"hostname"`
	URL              types.String `tfsdk:"url"`
	PrimaryLocation  types.String `tfsdk:"primary_location"`
	Regions          types.Set    `tfsdk:"regions"`
}

func NewLocationsDataSource() datasource.DataSource    { return &locationsDataSource{} }
func NewOrganizationDataSource() datasource.DataSource { return &organizationDataSource{} }
func NewGroupDataSource() datasource.DataSource        { return &groupDataSource{} }
func NewDatabaseDataSource() datasource.DataSource     { return &databaseDataSource{} }

func (d *locationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_locations"
}

func (d *locationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists current Turso location keys and human-readable names.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true},
			"locations": schema.MapAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *locationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if err := configureClient(req.ProviderData, &d.client); err != nil {
		resp.Diagnostics.AddError("Unexpected provider configuration", err.Error())
	}
}

func (d *locationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Turso client", "The provider client was not configured.")
		return
	}
	locations, err := d.client.ListLocations(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Turso locations", err.Error())
		return
	}
	locationMap, diagnostics := types.MapValueFrom(ctx, types.StringType, locations)
	resp.Diagnostics.Append(diagnostics...)
	state := locationsDataSourceModel{ID: types.StringValue("locations"), Locations: locationMap}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *organizationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *organizationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads nonsecret Turso organization configuration and plan identity.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true},
			"slug":           schema.StringAttribute{Optional: true, Computed: true, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must be a valid Turso organization slug")}},
			"name":           schema.StringAttribute{Computed: true},
			"type":           schema.StringAttribute{Computed: true},
			"plan_id":        schema.StringAttribute{Computed: true},
			"plan_timeline":  schema.StringAttribute{Computed: true},
			"overages":       schema.BoolAttribute{Computed: true},
			"require_mfa":    schema.BoolAttribute{Computed: true},
			"blocked_reads":  schema.BoolAttribute{Computed: true},
			"blocked_writes": schema.BoolAttribute{Computed: true},
			"platform":       schema.StringAttribute{Computed: true},
		},
	}
}

func (d *organizationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if err := configureClient(req.ProviderData, &d.client); err != nil {
		resp.Diagnostics.AddError("Unexpected provider configuration", err.Error())
	}
}

func (d *organizationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state organizationDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || d.client == nil {
		return
	}
	slug := state.Slug.ValueString()
	if slug == "" {
		slug = d.client.Organization()
	}
	organization, err := d.client.GetOrganization(ctx, slug)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Turso organization not found", "No organization matched the configured slug.")
		} else {
			resp.Diagnostics.AddError("Unable to read Turso organization", err.Error())
		}
		return
	}
	state.ID = types.StringValue(organization.Slug)
	state.Slug = types.StringValue(organization.Slug)
	state.Name = types.StringValue(organization.Name)
	state.Type = types.StringValue(organization.Type)
	state.PlanID = types.StringValue(organization.PlanID)
	state.PlanTimeline = types.StringValue(organization.PlanTimeline)
	state.Overages = types.BoolValue(organization.Overages)
	state.RequireMFA = types.BoolValue(organization.RequireMFA)
	state.BlockedReads = types.BoolValue(organization.BlockedReads)
	state.BlockedWrites = types.BoolValue(organization.BlockedWrites)
	state.Platform = types.StringValue(organization.Platform)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *groupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (d *groupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a Turso group.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true},
			"organization":      schema.StringAttribute{Optional: true, Computed: true, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must be a valid Turso organization slug")}},
			"name":              schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must be a valid Turso group name")}},
			"delete_protection": schema.BoolAttribute{Computed: true},
			"uuid":              schema.StringAttribute{Computed: true},
			"primary_location":  schema.StringAttribute{Computed: true},
			"locations":         schema.SetAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *groupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if err := configureClient(req.ProviderData, &d.client); err != nil {
		resp.Diagnostics.AddError("Unexpected provider configuration", err.Error())
	}
}

func (d *groupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state groupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || d.client == nil {
		return
	}
	org := state.Organization.ValueString()
	if org == "" {
		org = d.client.Organization()
	}
	group, err := d.client.GetGroup(ctx, org, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Turso group", err.Error())
		return
	}
	state.ID = types.StringValue(org + "/" + group.Name)
	state.Organization = types.StringValue(org)
	state.Name = types.StringValue(group.Name)
	state.DeleteProtection = types.BoolValue(group.DeleteProtection)
	state.UUID = types.StringValue(group.UUID)
	state.PrimaryLocation = types.StringValue(group.Primary)
	state.Locations = stringSet(ctx, group.Locations, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *databaseDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (d *databaseDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a Turso database without exposing auth tokens or contents.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true},
			"organization":      schema.StringAttribute{Optional: true, Computed: true, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must be a valid Turso organization slug")}},
			"name":              schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must be a valid Turso database name")}},
			"group":             schema.StringAttribute{Computed: true},
			"size_limit_bytes":  schema.Int64Attribute{Computed: true},
			"delete_protection": schema.BoolAttribute{Computed: true},
			"uuid":              schema.StringAttribute{Computed: true},
			"hostname":          schema.StringAttribute{Computed: true},
			"url":               schema.StringAttribute{Computed: true},
			"primary_location":  schema.StringAttribute{Computed: true},
			"regions":           schema.SetAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *databaseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if err := configureClient(req.ProviderData, &d.client); err != nil {
		resp.Diagnostics.AddError("Unexpected provider configuration", err.Error())
	}
}

func (d *databaseDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state databaseDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || d.client == nil {
		return
	}
	org := state.Organization.ValueString()
	if org == "" {
		org = d.client.Organization()
	}
	database, configuration, err := d.client.GetDatabase(ctx, org, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Turso database", err.Error())
		return
	}
	resourceState := databaseResourceModel{}
	setDatabaseResourceState(ctx, &resourceState, org, database, configuration, &resp.Diagnostics)
	state.ID = resourceState.ID
	state.Organization = resourceState.Organization
	state.Name = resourceState.Name
	state.Group = resourceState.Group
	state.SizeLimitBytes = resourceState.SizeLimitBytes
	state.DeleteProtection = resourceState.DeleteProtection
	state.UUID = resourceState.UUID
	state.Hostname = resourceState.Hostname
	state.URL = resourceState.URL
	state.PrimaryLocation = resourceState.PrimaryLocation
	state.Regions = resourceState.Regions
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
