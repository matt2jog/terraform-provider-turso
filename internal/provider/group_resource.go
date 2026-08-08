package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

var _ resource.Resource = &groupResource{}
var _ resource.ResourceWithConfigure = &groupResource{}
var _ resource.ResourceWithImportState = &groupResource{}

type groupResource struct{ client *client.Client }

type groupResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Organization     types.String `tfsdk:"organization"`
	Name             types.String `tfsdk:"name"`
	Location         types.String `tfsdk:"location"`
	DeleteProtection types.Bool   `tfsdk:"delete_protection"`
	UUID             types.String `tfsdk:"uuid"`
	PrimaryLocation  types.String `tfsdk:"primary_location"`
	Locations        types.Set    `tfsdk:"locations"`
}

func NewGroupResource() resource.Resource { return &groupResource{} }

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Turso database group. Disable delete_protection in a separate apply before destroying it.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, MarkdownDescription: "Stable organization/name identifier."},
			"organization":      schema.StringAttribute{Computed: true},
			"name":              schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must contain lowercase letters, digits, and dashes and be at most 64 characters")}},
			"location":          schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.RegexMatches(objectNamePattern, "must be a valid Turso location key")}, MarkdownDescription: "Initial primary location key."},
			"delete_protection": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"uuid":              schema.StringAttribute{Computed: true},
			"primary_location":  schema.StringAttribute{Computed: true},
			"locations":         schema.SetAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if err := configureClient(req.ProviderData, &r.client); err != nil {
		resp.Diagnostics.AddError("Unexpected provider configuration", err.Error())
	}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	org := r.client.Organization()
	desiredDeleteProtection := plan.DeleteProtection.ValueBool()
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	created, err := r.client.CreateGroup(opCtx, org, plan.Name.ValueString(), plan.Location.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Turso group", err.Error())
		return
	}
	if created.Name == "" {
		created.Name = plan.Name.ValueString()
	}
	if created.Primary == "" {
		created.Primary = plan.Location.ValueString()
	}
	setGroupResourceState(ctx, &plan, org, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.WaitForGroup(opCtx, org, plan.Name.ValueString(), true); err != nil {
		resp.Diagnostics.AddError("Turso group did not become ready", err.Error())
		return
	}
	if err := r.client.UpdateGroupConfiguration(opCtx, org, plan.Name.ValueString(), desiredDeleteProtection); err != nil {
		resp.Diagnostics.AddError("Unable to configure Turso group", err.Error())
		return
	}
	group, err := waitForGroupConfiguration(opCtx, r.client, org, plan.Name.ValueString(), desiredDeleteProtection)
	if err != nil {
		resp.Diagnostics.AddError("Turso group did not become ready", err.Error())
		return
	}
	setGroupResourceState(ctx, &plan, org, group, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	org := state.Organization.ValueString()
	if org == "" {
		org = r.client.Organization()
	}
	group, err := r.client.GetGroup(ctx, org, state.Name.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Turso group", err.Error())
		return
	}
	if state.Location.IsNull() || state.Location.IsUnknown() {
		location, err := canonicalGroupLocation(group)
		if err != nil {
			resp.Diagnostics.AddError(
				"Unable to determine the Turso group location",
				err.Error(),
			)
			return
		}
		state.Location = types.StringValue(location)
	}
	setGroupResourceState(ctx, &state, org, group, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// canonicalGroupLocation returns a location key accepted by Turso's create API.
// The API's primary field may contain a region name (for example us-east-1)
// while locations contains the canonical provider key (aws-us-east-1).
func canonicalGroupLocation(group *client.Group) (string, error) {
	if group == nil {
		return "", errors.New("the Turso API returned an empty group")
	}
	primary := strings.TrimSpace(group.Primary)
	for _, location := range group.Locations {
		if location == primary {
			return location, nil
		}
	}
	for _, location := range group.Locations {
		if primary != "" && strings.HasSuffix(location, "-"+primary) {
			return location, nil
		}
	}
	if len(group.Locations) == 1 && group.Locations[0] != "" {
		return group.Locations[0], nil
	}
	return "", fmt.Errorf("cannot map Turso primary location %q to canonical location keys %v", primary, group.Locations)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	org := plan.Organization.ValueString()
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := r.client.UpdateGroupConfiguration(opCtx, org, plan.Name.ValueString(), plan.DeleteProtection.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Unable to update Turso group", err.Error())
		return
	}
	group, err := waitForGroupConfiguration(opCtx, r.client, org, plan.Name.ValueString(), plan.DeleteProtection.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Unable to refresh Turso group", err.Error())
		return
	}
	setGroupResourceState(ctx, &plan, org, group, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || r.client == nil {
		return
	}
	if state.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError("Turso group is protected", "Set delete_protection = false and apply that change before destroying this group.")
		return
	}
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := r.client.DeleteGroup(opCtx, state.Organization.ValueString(), state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Unable to delete Turso group", err.Error())
		return
	}
	if _, err := r.client.WaitForGroup(opCtx, state.Organization.ValueString(), state.Name.ValueString(), false); err != nil {
		resp.Diagnostics.AddError("Turso group deletion was not confirmed", err.Error())
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	org, name, err := parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Turso group import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization"), org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func setGroupResourceState(ctx context.Context, state *groupResourceModel, org string, group *client.Group, diagnostics *diag.Diagnostics) {
	state.ID = types.StringValue(org + "/" + group.Name)
	state.Organization = types.StringValue(org)
	state.Name = types.StringValue(group.Name)
	state.DeleteProtection = types.BoolValue(group.DeleteProtection)
	state.UUID = types.StringValue(group.UUID)
	state.PrimaryLocation = types.StringValue(group.Primary)
	state.Locations = stringSet(ctx, group.Locations, diagnostics)
}
