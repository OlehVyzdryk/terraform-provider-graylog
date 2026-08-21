package provider

import (
	"context"
	"errors"
	"time"

	"github.com/Ultrafenrir/terraform-provider-graylog/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type authBackendResource struct{ client *client.Client }

type authBackendModel struct {
	ID           types.String   `tfsdk:"id"`
	Title        types.String   `tfsdk:"title"`
	Description  types.String   `tfsdk:"description"`
	DefaultRoles types.Set      `tfsdk:"default_roles"`
	ConfigJSON   types.String   `tfsdk:"config_json"`
	Password     types.String   `tfsdk:"system_user_password"`
	Timeouts     timeouts.Value `tfsdk:"timeouts"`
}

func NewAuthBackendResource() resource.Resource { return &authBackendResource{} }

func (r *authBackendResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_auth_backend"
}

func (r *authBackendResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version: 1,
		Description: "Manages a Graylog authentication service backend (LDAP or Active Directory) via " +
			"`/system/authentication/services/backends`. Creating a backend does not activate it; use " +
			"`graylog_auth_backend_activation` for that.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Authentication backend ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"title":       schema.StringAttribute{Required: true, Description: "Backend title."},
			"description": schema.StringAttribute{Optional: true, Description: "Backend description."},
			"default_roles": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Role IDs granted to every user authenticated by this backend. Unordered: Graylog does not guarantee the order it returns them in.",
			},
			"config_json": schema.StringAttribute{
				Required: true,
				Description: "Backend configuration, JSON-encoded, including the `type` discriminator " +
					"(`ldap` or `active-directory`). The bind password belongs in `system_user_password`, " +
					"not here. Keys the server adds on its own, such as `email_attributes`, take no part in " +
					"drift detection.",
			},
			"system_user_password": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Password for the system user that binds to the directory. Write-only: Graylog " +
					"reports it as `{\"is_set\": true}` rather than returning the value, so it cannot be " +
					"refreshed and a change made outside Terraform is invisible here. Omit the attribute to " +
					"keep whatever password is already stored, which is also the state an imported backend " +
					"starts in; an empty string is rejected rather than quietly meaning the same thing.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *authBackendResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *authBackendResource) roles(ctx context.Context, data *authBackendModel) ([]string, error) {
	if data.DefaultRoles.IsNull() || data.DefaultRoles.IsUnknown() {
		return []string{}, nil
	}
	var roles []string
	if diags := data.DefaultRoles.ElementsAs(ctx, &roles, false); diags.HasError() {
		return nil, errors.New("default_roles must be a set of strings")
	}
	return roles, nil
}

func (r *authBackendResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data authBackendModel
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

	config, err := buildAuthBackendConfig(data.ConfigJSON.ValueString(), data.Password.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid authentication backend configuration", err.Error())
		return
	}
	roles, err := r.roles(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid authentication backend configuration", err.Error())
		return
	}

	created, err := r.client.WithContext(ctx).CreateAuthBackend(&client.AuthBackend{
		Title:        data.Title.ValueString(),
		Description:  data.Description.ValueString(),
		DefaultRoles: roles,
		Config:       config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating authentication backend", err.Error())
		return
	}

	// config_json keeps the planned value: the echo replaces the password
	// with a sentinel and adds server-side defaults.
	data.ID = types.StringValue(created.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *authBackendResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data authBackendModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	backend, err := r.client.WithContext(ctx).GetAuthBackend(data.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading authentication backend", err.Error())
		return
	}

	data.ID = types.StringValue(backend.ID)
	data.Title = types.StringValue(backend.Title)
	if !data.Description.IsNull() || backend.Description != "" {
		data.Description = types.StringValue(backend.Description)
	}
	if !data.DefaultRoles.IsNull() || len(backend.DefaultRoles) > 0 {
		roles, diags := types.SetValueFrom(ctx, types.StringType, backend.DefaultRoles)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.DefaultRoles = roles
	}

	// system_user_password is deliberately left as-is: it cannot be read
	// back, so state is the only place it exists.
	refreshed, err := refreshAuthBackendConfig(string(backend.Config), data.ConfigJSON.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading authentication backend", err.Error())
		return
	}
	// An unparseable or absent state document — an import, or state written by
	// an older version — is not comparable; an empty string forces the
	// refreshed value to be adopted rather than failing the read.
	stateConfig, err := CanonicalizeJSONFromString(data.ConfigJSON.ValueString())
	if err != nil {
		stateConfig = ""
	}
	if refreshed != stateConfig {
		data.ConfigJSON = types.StringValue(refreshed)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *authBackendResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state authBackendModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateTimeout, diags := plan.Timeouts.Update(ctx, 5*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	config, err := buildAuthBackendConfig(plan.ConfigJSON.ValueString(), plan.Password.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid authentication backend configuration", err.Error())
		return
	}
	roles, err := r.roles(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid authentication backend configuration", err.Error())
		return
	}

	// The ID comes from state: it is Computed and therefore unknown in the
	// plan whenever another attribute changes.
	id := state.ID.ValueString()
	if _, err := r.client.WithContext(ctx).UpdateAuthBackend(id, &client.AuthBackend{
		Title:        plan.Title.ValueString(),
		Description:  plan.Description.ValueString(),
		DefaultRoles: roles,
		Config:       config,
	}); err != nil {
		resp.Diagnostics.AddError("Error updating authentication backend", err.Error())
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *authBackendResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data authBackendModel
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

	if err := r.client.WithContext(ctx).DeleteAuthBackend(data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deleting authentication backend", err.Error())
	}
}

func (r *authBackendResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Read adopts the server's configuration wholesale for an imported
	// backend. The password is not part of it and has to be added to the
	// configuration before the next apply.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
