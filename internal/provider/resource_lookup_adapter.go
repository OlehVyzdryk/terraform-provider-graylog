package provider

import (
	"context"
	"errors"
	"fmt"
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

type lookupAdapterResource struct{ client *client.Client }

type lookupAdapterModel struct {
	ID          types.String   `tfsdk:"id"`
	Name        types.String   `tfsdk:"name"`
	Title       types.String   `tfsdk:"title"`
	Description types.String   `tfsdk:"description"`
	ConfigJSON  types.String   `tfsdk:"config_json"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func NewLookupAdapterResource() resource.Resource { return &lookupAdapterResource{} }

func (r *lookupAdapterResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_lookup_adapter"
}

func (r *lookupAdapterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages a Graylog lookup data adapter, the backing data source of a lookup table.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Lookup data adapter ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Unique name, referenced by `graylog_lookup_table.data_adapter_id` consumers. Renaming is an in-place update.",
			},
			"title":       schema.StringAttribute{Required: true, Description: "Human readable title."},
			"description": schema.StringAttribute{Optional: true, Description: "Adapter description."},
			"config_json": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				Description: "Adapter configuration, JSON-encoded, including the `type` discriminator " +
					"(for example `csvfile`, `maxmind_geoip`, `httpjsonpath`). Graylog fills in the " +
					"defaults of the chosen type, and those extra keys are ignored for drift detection — " +
					"only the keys present here are compared against the server. Marked sensitive because " +
					"adapter configurations routinely carry credentials.",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *lookupAdapterResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *lookupAdapterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data lookupAdapterModel
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

	config, err := validatedLookupConfig(data.ConfigJSON.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid lookup data adapter configuration", err.Error())
		return
	}

	created, err := r.client.WithContext(ctx).CreateLookupAdapter(&client.LookupAdapter{
		Name:        data.Name.ValueString(),
		Title:       data.Title.ValueString(),
		Description: data.Description.ValueString(),
		Config:      config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating lookup data adapter", err.Error())
		return
	}

	// config_json keeps the planned value: the echo carries the defaults of
	// the cache type, and writing those back would change a Required
	// attribute after apply.
	data.ID = types.StringValue(created.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *lookupAdapterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data lookupAdapterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	adapter, err := r.client.WithContext(ctx).GetLookupAdapter(data.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading lookup data adapter", err.Error())
		return
	}

	// An import may have been addressed by name; store the resolved ID so
	// later updates and deletes never depend on name resolution.
	data.ID = types.StringValue(adapter.ID)
	data.Name = types.StringValue(adapter.Name)
	data.Title = types.StringValue(adapter.Title)
	if !data.Description.IsNull() || adapter.Description != "" {
		data.Description = types.StringValue(adapter.Description)
	}

	projected, err := ProjectAndCanonicalizeJSON(string(adapter.Config), data.ConfigJSON.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading lookup data adapter",
			fmt.Sprintf("server returned a configuration that is not valid JSON: %v", err))
		return
	}
	stateConfig, err := CanonicalizeJSONFromString(data.ConfigJSON.ValueString())
	if err != nil {
		stateConfig = ""
	}
	if projected != stateConfig {
		data.ConfigJSON = types.StringValue(projected)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *lookupAdapterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lookupAdapterModel
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

	config, err := validatedLookupConfig(plan.ConfigJSON.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid lookup data adapter configuration", err.Error())
		return
	}

	// The ID comes from state: it is Computed, so the plan carries it as
	// unknown on any change that touches another attribute.
	id := state.ID.ValueString()
	if _, err := r.client.WithContext(ctx).UpdateLookupAdapter(id, &client.LookupAdapter{
		Name:        plan.Name.ValueString(),
		Title:       plan.Title.ValueString(),
		Description: plan.Description.ValueString(),
		Config:      config,
	}); err != nil {
		resp.Diagnostics.AddError("Error updating lookup data adapter", err.Error())
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lookupAdapterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data lookupAdapterModel
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

	// Graylog refuses to delete a cache a lookup table still references, so
	// the error is surfaced rather than swallowed.
	if err := r.client.WithContext(ctx).DeleteLookupAdapter(data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deleting lookup data adapter", err.Error())
	}
}

func (r *lookupAdapterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Accepts an ID or a name; Read replaces it with the resolved ID.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
