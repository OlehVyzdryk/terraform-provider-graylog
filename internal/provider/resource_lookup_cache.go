package provider

import (
	"context"
	"encoding/json"
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

type lookupCacheResource struct{ client *client.Client }

type lookupCacheModel struct {
	ID          types.String   `tfsdk:"id"`
	Name        types.String   `tfsdk:"name"`
	Title       types.String   `tfsdk:"title"`
	Description types.String   `tfsdk:"description"`
	ConfigJSON  types.String   `tfsdk:"config_json"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func NewLookupCacheResource() resource.Resource { return &lookupCacheResource{} }

func (r *lookupCacheResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_lookup_cache"
}

func (r *lookupCacheResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages a Graylog lookup cache, the caching half of a lookup table.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Lookup cache ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Unique name, referenced by `graylog_lookup_table.cache_id` consumers. Renaming is an in-place update.",
			},
			"title":       schema.StringAttribute{Required: true, Description: "Human readable title."},
			"description": schema.StringAttribute{Optional: true, Description: "Cache description."},
			"config_json": schema.StringAttribute{
				Required: true,
				Description: "Cache configuration, JSON-encoded, including the `type` discriminator " +
					"(for example `guava_cache`). Graylog fills in the defaults of the chosen type, and " +
					"those extra keys are ignored for drift detection — only the keys present here are " +
					"compared against the server.",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *lookupCacheResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

// validatedLookupConfig rejects anything that is not a JSON object before the
// round trip; Graylog always deserializes the config into a typed class.
func validatedLookupConfig(raw string) (json.RawMessage, error) {
	if raw == "" {
		return nil, errors.New("config_json must not be empty")
	}
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("config_json is not valid JSON: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, errors.New("config_json must be a JSON object")
	}
	return json.RawMessage(raw), nil
}

func (r *lookupCacheResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data lookupCacheModel
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
		resp.Diagnostics.AddError("Invalid lookup cache configuration", err.Error())
		return
	}

	created, err := r.client.WithContext(ctx).CreateLookupCache(&client.LookupCache{
		Name:        data.Name.ValueString(),
		Title:       data.Title.ValueString(),
		Description: data.Description.ValueString(),
		Config:      config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating lookup cache", err.Error())
		return
	}

	// config_json keeps the planned value: the echo carries the defaults of
	// the cache type, and writing those back would change a Required
	// attribute after apply.
	data.ID = types.StringValue(created.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *lookupCacheResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data lookupCacheModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cache, err := r.client.WithContext(ctx).GetLookupCache(data.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading lookup cache", err.Error())
		return
	}

	// An import may have been addressed by name; store the resolved ID so
	// later updates and deletes never depend on name resolution.
	data.ID = types.StringValue(cache.ID)
	data.Name = types.StringValue(cache.Name)
	data.Title = types.StringValue(cache.Title)
	if !data.Description.IsNull() || cache.Description != "" {
		data.Description = types.StringValue(cache.Description)
	}

	projected, err := ProjectAndCanonicalizeJSON(string(cache.Config), data.ConfigJSON.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading lookup cache",
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

func (r *lookupCacheResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lookupCacheModel
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
		resp.Diagnostics.AddError("Invalid lookup cache configuration", err.Error())
		return
	}

	// The ID comes from state: it is Computed, so the plan carries it as
	// unknown on any change that touches another attribute.
	id := state.ID.ValueString()
	if _, err := r.client.WithContext(ctx).UpdateLookupCache(id, &client.LookupCache{
		Name:        plan.Name.ValueString(),
		Title:       plan.Title.ValueString(),
		Description: plan.Description.ValueString(),
		Config:      config,
	}); err != nil {
		resp.Diagnostics.AddError("Error updating lookup cache", err.Error())
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lookupCacheResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data lookupCacheModel
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
	if err := r.client.WithContext(ctx).DeleteLookupCache(data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deleting lookup cache", err.Error())
	}
}

func (r *lookupCacheResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Accepts an ID or a name; Read replaces it with the resolved ID.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
