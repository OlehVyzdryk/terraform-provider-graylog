package provider

import (
	"context"
	"errors"
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

type pipelineConnectionResource struct{ client *client.Client }

type pipelineConnectionModel struct {
	ID          types.String   `tfsdk:"id"`
	StreamID    types.String   `tfsdk:"stream_id"`
	PipelineIDs types.Set      `tfsdk:"pipeline_ids"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func NewPipelineConnectionResource() resource.Resource { return &pipelineConnectionResource{} }

func (r *pipelineConnectionResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "graylog_pipeline_connection"
}

func (r *pipelineConnectionResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages the set of pipelines connected to a stream. The resource owns the full connection list for its stream.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Computed: true, Description: "Same as stream_id", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"stream_id":    schema.StringAttribute{Required: true, Description: "Stream ID to connect pipelines to", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"pipeline_ids": schema.SetAttribute{Required: true, ElementType: types.StringType, Description: "Pipeline IDs connected to the stream"},
			"timeouts":     timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *pipelineConnectionResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*client.Client)
}

func (r *pipelineConnectionResource) apply(ctx context.Context, data *pipelineConnectionModel) error {
	var pids []string
	if !data.PipelineIDs.IsNull() && !data.PipelineIDs.IsUnknown() {
		if diags := data.PipelineIDs.ElementsAs(ctx, &pids, false); diags.HasError() {
			return errors.New("invalid pipeline_ids")
		}
	}
	_, err := r.client.WithContext(ctx).SetPipelineConnection(&client.PipelineConnection{
		StreamID:    data.StreamID.ValueString(),
		PipelineIDs: pids,
	})
	return err
}

func (r *pipelineConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data pipelineConnectionModel
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

	if err := r.apply(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Error connecting pipelines to stream", err.Error())
		return
	}
	data.ID = types.StringValue(data.StreamID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data pipelineConnectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := r.client.WithContext(ctx).GetPipelineConnection(data.StreamID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading pipeline connection", err.Error())
		return
	}
	if len(conn.PipelineIDs) == 0 {
		// All pipelines were disconnected out of band; treat as deleted.
		resp.State.RemoveResource(ctx)
		return
	}
	pv := make([]types.String, 0, len(conn.PipelineIDs))
	for _, id := range conn.PipelineIDs {
		pv = append(pv, types.StringValue(id))
	}
	set, diags := types.SetValueFrom(ctx, types.StringType, pv)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.PipelineIDs = set
	data.ID = types.StringValue(data.StreamID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data pipelineConnectionModel
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

	if err := r.apply(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Error updating pipeline connection", err.Error())
		return
	}
	data.ID = types.StringValue(data.StreamID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data pipelineConnectionModel
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

	_, err := r.client.WithContext(ctx).SetPipelineConnection(&client.PipelineConnection{
		StreamID:    data.StreamID.ValueString(),
		PipelineIDs: []string{},
	})
	if err != nil {
		resp.Diagnostics.AddError("Error disconnecting pipelines from stream", err.Error())
	}
}

func (r *pipelineConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by stream id.
	resource.ImportStatePassthroughID(ctx, path.Root("stream_id"), req, resp)
}
