package provider

import (
	"context"
	"errors"
	"time"

	"github.com/Ultrafenrir/terraform-provider-graylog/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type pipelineRuleResource struct{ client *client.Client }

type pipelineRuleModel struct {
	ID          types.String   `tfsdk:"id"`
	Title       types.String   `tfsdk:"title"`
	Description types.String   `tfsdk:"description"`
	Source      types.String   `tfsdk:"source"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func NewPipelineRuleResource() resource.Resource { return &pipelineRuleResource{} }

func (r *pipelineRuleResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_pipeline_rule"
}

func (r *pipelineRuleResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages a Graylog pipeline rule. Pipelines reference rules by the name declared in the rule source.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Pipeline rule ID", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"title":       schema.StringAttribute{Computed: true, Description: "Rule title; Graylog derives it from the rule name declared in source", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"description": schema.StringAttribute{Optional: true, Description: "Rule description"},
			"source":      schema.StringAttribute{Required: true, Description: "Rule source (pipeline rule DSL: rule \"name\" when ... then ... end)"},
			"timeouts":    timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *pipelineRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *pipelineRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data pipelineRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validatePipelineRule(&data)...)
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

	created, err := r.client.WithContext(ctx).CreatePipelineRule(&client.PipelineRule{
		Description: data.Description.ValueString(),
		Source:      data.Source.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating pipeline rule", err.Error())
		return
	}
	data.ID = types.StringValue(created.ID)
	data.Title = types.StringValue(created.Title)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data pipelineRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.WithContext(ctx).GetPipelineRule(data.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading pipeline rule", err.Error())
		return
	}
	data.Title = types.StringValue(rule.Title)
	if !data.Description.IsNull() || rule.Description != "" {
		data.Description = types.StringValue(rule.Description)
	}
	data.Source = types.StringValue(rule.Source)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data pipelineRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validatePipelineRule(&data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The id is Computed and may be unknown in the plan; take it from state.
	var state pipelineRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = state.ID

	updateTimeout, diags := data.Timeouts.Update(ctx, 5*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	updated, err := r.client.WithContext(ctx).UpdatePipelineRule(state.ID.ValueString(), &client.PipelineRule{
		ID:          state.ID.ValueString(),
		Description: data.Description.ValueString(),
		Source:      data.Source.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error updating pipeline rule", err.Error())
		return
	}
	data.Title = types.StringValue(updated.Title)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data pipelineRuleModel
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

	if err := r.client.WithContext(ctx).DeletePipelineRule(data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deleting pipeline rule", err.Error())
	}
}

func (r *pipelineRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// validatePipelineRule checks that the rule source is present.
func validatePipelineRule(m *pipelineRuleModel) (d diag.Diagnostics) {
	if m.Source.IsNull() || m.Source.IsUnknown() || m.Source.ValueString() == "" {
		d.AddAttributeError(path.Root("source"), "Invalid source", "Attribute 'source' must contain the rule DSL (rule \"name\" when ... then ... end).")
	}
	return
}
