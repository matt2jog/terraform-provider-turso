package provider

import (
	"context"
	"errors"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

var _ resource.Resource = &databaseResource{}
var _ resource.ResourceWithConfigure = &databaseResource{}
var _ resource.ResourceWithImportState = &databaseResource{}

type databaseResource struct{ client *client.Client }

type databaseResourceModel struct {
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

func NewDatabaseResource() resource.Resource { return &databaseResource{} }

func (r *databaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (r *databaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	nameValidators := []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must contain lowercase letters, digits, and dashes and be at most 64 characters")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Turso database. Database auth tokens are created outside Terraform. Disable delete_protection in a separate apply before destroying it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Stable organization/name identifier."},
			"organization": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name":              schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: nameValidators},
			"group":             schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: nameValidators},
			"size_limit_bytes":  schema.Int64Attribute{Optional: true, Computed: true, Validators: []validator.Int64{int64validator.AtLeast(1)}, MarkdownDescription: "Maximum database size in bytes. Uses Turso's account default when omitted."},
			"delete_protection": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"uuid":              schema.StringAttribute{Computed: true},
			"hostname":          schema.StringAttribute{Computed: true},
			"url":               schema.StringAttribute{Computed: true, MarkdownDescription: "Nonsecret libsql:// database URL."},
			"primary_location":  schema.StringAttribute{Computed: true},
			"regions":           schema.SetAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (r *databaseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if err := configureClient(req.ProviderData, &r.client); err != nil {
		resp.Diagnostics.AddError("Unexpected provider configuration", err.Error())
	}
}

func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan databaseResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	org := r.client.Organization()
	desiredDeleteProtection := plan.DeleteProtection.ValueBool()
	sizeLimit := ""
	desiredSizeLimit := int64(0)
	checkSizeLimit := false
	if !plan.SizeLimitBytes.IsNull() && !plan.SizeLimitBytes.IsUnknown() {
		desiredSizeLimit = plan.SizeLimitBytes.ValueInt64()
		checkSizeLimit = true
		sizeLimit = strconv.FormatInt(desiredSizeLimit, 10)
	}
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	created, err := r.client.CreateDatabase(opCtx, org, plan.Name.ValueString(), plan.Group.ValueString(), sizeLimit)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Turso database", err.Error())
		return
	}
	if created.Name == "" {
		created.Name = plan.Name.ValueString()
	}
	if created.Group == "" {
		created.Group = plan.Group.ValueString()
	}
	setDatabaseResourceState(ctx, &plan, org, created, nil, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, _, err := r.client.WaitForDatabase(opCtx, org, plan.Name.ValueString(), true); err != nil {
		resp.Diagnostics.AddError("Turso database did not become ready", err.Error())
		return
	}
	if err := r.client.UpdateDatabaseConfiguration(opCtx, org, plan.Name.ValueString(), sizeLimit, desiredDeleteProtection); err != nil {
		resp.Diagnostics.AddError("Unable to configure Turso database", err.Error())
		return
	}
	database, configuration, err := waitForDatabaseConfiguration(opCtx, r.client, org, plan.Name.ValueString(), desiredSizeLimit, checkSizeLimit, desiredDeleteProtection)
	if err != nil {
		resp.Diagnostics.AddError("Turso database did not become ready", err.Error())
		return
	}
	setDatabaseResourceState(ctx, &plan, org, database, configuration, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state databaseResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	org := state.Organization.ValueString()
	if org == "" {
		org = r.client.Organization()
	}
	database, configuration, err := r.client.GetDatabase(ctx, org, state.Name.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Turso database", err.Error())
		return
	}
	setDatabaseResourceState(ctx, &state, org, database, configuration, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *databaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan databaseResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	org := plan.Organization.ValueString()
	if plan.Organization.IsNull() || plan.Organization.IsUnknown() || org == "" {
		var state databaseResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		org = state.Organization.ValueString()
	}
	if org == "" {
		org = r.client.Organization()
	}
	sizeLimit := strconv.FormatInt(plan.SizeLimitBytes.ValueInt64(), 10)
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := r.client.UpdateDatabaseConfiguration(opCtx, org, plan.Name.ValueString(), sizeLimit, plan.DeleteProtection.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Unable to update Turso database", err.Error())
		return
	}
	database, configuration, err := waitForDatabaseConfiguration(opCtx, r.client, org, plan.Name.ValueString(), plan.SizeLimitBytes.ValueInt64(), true, plan.DeleteProtection.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Unable to refresh Turso database", err.Error())
		return
	}
	setDatabaseResourceState(ctx, &plan, org, database, configuration, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state databaseResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	if state.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError("Turso database is protected", "Set delete_protection = false and apply that change before destroying this database.")
		return
	}
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := r.client.DeleteDatabase(opCtx, state.Organization.ValueString(), state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Unable to delete Turso database", err.Error())
		return
	}
	if _, _, err := r.client.WaitForDatabase(opCtx, state.Organization.ValueString(), state.Name.ValueString(), false); err != nil {
		resp.Diagnostics.AddError("Turso database deletion was not confirmed", err.Error())
	}
}

func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	org, name, err := parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Turso database import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization"), org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func setDatabaseResourceState(ctx context.Context, state *databaseResourceModel, org string, database *client.Database, configuration *client.DatabaseConfiguration, diagnostics *diag.Diagnostics) {
	state.ID = types.StringValue(org + "/" + database.Name)
	state.Organization = types.StringValue(org)
	state.Name = types.StringValue(database.Name)
	state.Group = types.StringValue(database.Group)
	state.UUID = types.StringValue(database.UUID)
	state.Hostname = types.StringValue(database.Hostname)
	state.URL = types.StringValue("libsql://" + database.Hostname)
	state.PrimaryLocation = types.StringValue(database.PrimaryRegion)
	state.Regions = stringSet(ctx, database.Regions, diagnostics)
	if configuration != nil {
		state.DeleteProtection = types.BoolValue(configuration.DeleteProtection)
		size, err := parseSizeLimit(configuration.SizeLimit)
		if err != nil {
			diagnostics.AddError("Invalid Turso size limit response", err.Error())
		} else if size > 0 {
			state.SizeLimitBytes = types.Int64Value(size)
		}
	} else {
		state.DeleteProtection = types.BoolValue(database.DeleteProtection)
	}
}
