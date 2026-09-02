package provider

import (
	"context"
	"time"

	"github.com/Ultrafenrir/terraform-provider-graylog/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// activeAuthBackendID is the fixed Terraform ID of the activation resource.
// Graylog stores exactly one active backend for the whole cluster, so the
// resource is a singleton and its ID carries no information.
const activeAuthBackendID = "active"

type authBackendActivationResource struct{ client *client.Client }

type authBackendActivationModel struct {
	ID        types.String   `tfsdk:"id"`
	BackendID types.String   `tfsdk:"backend_id"`
	Timeouts  timeouts.Value `tfsdk:"timeouts"`
}

func NewAuthBackendActivationResource() resource.Resource {
	return &authBackendActivationResource{}
}

func (r *authBackendActivationResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_auth_backend_activation"
}

func (r *authBackendActivationResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version: 1,
		Description: "Selects the cluster's active authentication backend. Graylog stores one active " +
			"backend for the whole cluster, so this resource is a singleton: declaring it twice means two " +
			"resources fighting over the same setting. Destroying it returns the cluster to local " +
			"authentication only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Always `active`; the setting is cluster-wide and has no identity of its own.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"backend_id": schema.StringAttribute{
				Required:    true,
				Description: "ID of the `graylog_auth_backend` to activate.",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *authBackendActivationResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *authBackendActivationResource) activate(ctx context.Context, data *authBackendActivationModel) error {
	if err := r.client.WithContext(ctx).SetActiveAuthBackend(data.BackendID.ValueString()); err != nil {
		return err
	}
	data.ID = types.StringValue(activeAuthBackendID)
	return nil
}

func (r *authBackendActivationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data authBackendActivationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createTimeout, diags := data.Timeouts.Create(ctx, 5*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	if err := r.activate(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Error activating authentication backend", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *authBackendActivationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data authBackendActivationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	active, err := r.client.WithContext(ctx).GetActiveAuthBackend()
	if err != nil {
		resp.Diagnostics.AddError("Error reading authentication configuration", err.Error())
		return
	}
	if active == "" {
		// Nothing is active any more, so there is no selection to manage.
		resp.State.RemoveResource(ctx)
		return
	}

	data.ID = types.StringValue(activeAuthBackendID)
	data.BackendID = types.StringValue(active)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *authBackendActivationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data authBackendActivationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateTimeout, diags := data.Timeouts.Update(ctx, 5*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	if err := r.activate(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Error activating authentication backend", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *authBackendActivationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data authBackendActivationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteTimeout, diags := data.Timeouts.Delete(ctx, 3*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	// Only clear the selection if it is still the one this resource put
	// there. If something else has since activated a different backend,
	// destroying this resource must not take that down with it.
	active, err := r.client.WithContext(ctx).GetActiveAuthBackend()
	if err != nil {
		resp.Diagnostics.AddError("Error reading the active authentication backend", err.Error())
		return
	}
	if active != data.BackendID.ValueString() {
		return
	}

	// Clearing the selection leaves local authentication in place; the
	// backend itself is untouched.
	if err := r.client.WithContext(ctx).SetActiveAuthBackend(""); err != nil {
		resp.Diagnostics.AddError("Error clearing the active authentication backend", err.Error())
	}
}

func (r *authBackendActivationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), activeAuthBackendID)...)
}
