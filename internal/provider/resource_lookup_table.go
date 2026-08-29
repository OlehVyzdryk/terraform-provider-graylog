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

type lookupTableResource struct{ client *client.Client }

type lookupTableModel struct {
	ID                     types.String   `tfsdk:"id"`
	Name                   types.String   `tfsdk:"name"`
	Title                  types.String   `tfsdk:"title"`
	Description            types.String   `tfsdk:"description"`
	CacheID                types.String   `tfsdk:"cache_id"`
	DataAdapterID          types.String   `tfsdk:"data_adapter_id"`
	DefaultSingleValue     types.String   `tfsdk:"default_single_value"`
	DefaultSingleValueType types.String   `tfsdk:"default_single_value_type"`
	DefaultMultiValue      types.String   `tfsdk:"default_multi_value"`
	DefaultMultiValueType  types.String   `tfsdk:"default_multi_value_type"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

func NewLookupTableResource() resource.Resource { return &lookupTableResource{} }

func (r *lookupTableResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_lookup_table"
}

func (r *lookupTableResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version: 1,
		Description: "Manages a Graylog lookup table, which binds a cache and a data adapter under the name " +
			"that pipeline rules resolve with `lookup()` and `lookup_value()`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Lookup table ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "Unique name. This is the string pipeline rules pass to `lookup()`, so renaming a " +
					"table stops every rule that referenced the old name from resolving.",
			},
			"title":           schema.StringAttribute{Required: true, Description: "Human readable title."},
			"description":     schema.StringAttribute{Optional: true, Description: "Lookup table description."},
			"cache_id":        schema.StringAttribute{Required: true, Description: "ID of the `graylog_lookup_cache` to use."},
			"data_adapter_id": schema.StringAttribute{Required: true, Description: "ID of the `graylog_lookup_adapter` to use."},
			"default_single_value": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Value returned for a single-value lookup that finds nothing.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"default_single_value_type": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Type of `default_single_value`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{
					stringvalidator.OneOf("STRING", "NUMBER", "BOOLEAN", "OBJECT", "NULL"),
				},
			},
			"default_multi_value": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Value returned for a multi-value lookup that finds nothing.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"default_multi_value_type": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Type of `default_multi_value`. Graylog only accepts `OBJECT` or `NULL` here.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{
					stringvalidator.OneOf("OBJECT", "NULL"),
				},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *lookupTableResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

// defaultOr returns the configured value, or the fallback when the attribute
// was left out. Graylog requires all four default-value fields in the body
// and echoes "" / "NULL" when they are unused.
func defaultOr(v types.String, fallback string) string {
	if v.IsNull() || v.IsUnknown() {
		return fallback
	}
	return v.ValueString()
}

func (r *lookupTableResource) payload(data *lookupTableModel) *client.LookupTable {
	return &client.LookupTable{
		Name:                   data.Name.ValueString(),
		Title:                  data.Title.ValueString(),
		Description:            data.Description.ValueString(),
		CacheID:                data.CacheID.ValueString(),
		DataAdapterID:          data.DataAdapterID.ValueString(),
		DefaultSingleValue:     defaultOr(data.DefaultSingleValue, ""),
		DefaultSingleValueType: defaultOr(data.DefaultSingleValueType, "NULL"),
		DefaultMultiValue:      defaultOr(data.DefaultMultiValue, ""),
		DefaultMultiValueType:  defaultOr(data.DefaultMultiValueType, "NULL"),
	}
}

// applyReadState copies the server object into the model. The four
// default-value attributes are Optional+Computed, so they always take the
// server value; description keeps whichever "unset" representation the state
// already used, so a null never flips to "" and back on every plan.
func applyLookupTableReadState(data *lookupTableModel, table *client.LookupTable) {
	data.ID = types.StringValue(table.ID)
	data.Name = types.StringValue(table.Name)
	data.Title = types.StringValue(table.Title)
	if !data.Description.IsNull() || table.Description != "" {
		data.Description = types.StringValue(table.Description)
	}
	data.CacheID = types.StringValue(table.CacheID)
	data.DataAdapterID = types.StringValue(table.DataAdapterID)
	data.DefaultSingleValue = types.StringValue(table.DefaultSingleValue)
	data.DefaultSingleValueType = types.StringValue(table.DefaultSingleValueType)
	data.DefaultMultiValue = types.StringValue(table.DefaultMultiValue)
	data.DefaultMultiValueType = types.StringValue(table.DefaultMultiValueType)
}

func (r *lookupTableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data lookupTableModel
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

	created, err := r.client.WithContext(ctx).CreateLookupTable(r.payload(&data))
	if err != nil {
		resp.Diagnostics.AddError("Error creating lookup table", err.Error())
		return
	}

	applyLookupTableReadState(&data, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *lookupTableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data lookupTableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	table, err := r.client.WithContext(ctx).GetLookupTable(data.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading lookup table", err.Error())
		return
	}

	// An import may have been addressed by name; the resolved ID replaces it.
	applyLookupTableReadState(&data, table)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *lookupTableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lookupTableModel
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

	// The ID comes from state: it is Computed, so the plan carries it as
	// unknown on any change that touches another attribute.
	id := state.ID.ValueString()
	updated, err := r.client.WithContext(ctx).UpdateLookupTable(id, r.payload(&plan))
	if err != nil {
		resp.Diagnostics.AddError("Error updating lookup table", err.Error())
		return
	}

	applyLookupTableReadState(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lookupTableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data lookupTableModel
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

	if err := r.client.WithContext(ctx).DeleteLookupTable(data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deleting lookup table", err.Error())
	}
}

func (r *lookupTableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Accepts an ID or a name; Read replaces it with the resolved ID.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
