// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	_ resource.ResourceWithConfigure        = (*configResource)(nil)
	_ resource.ResourceWithImportState      = (*configResource)(nil)
	_ resource.ResourceWithConfigValidators = (*configResource)(nil)
	_ resource.ResourceWithValidateConfig   = (*configResource)(nil)
)

const privateInferKey = "infer_kind"

type configResource struct {
	client *client.Client
}

type configModel struct {
	ID     types.String         `tfsdk:"id"`
	Path   types.List           `tfsdk:"path"`
	Value  types.String         `tfsdk:"value"`
	Values types.List           `tfsdk:"values"`
	Tree   jsontypes.Normalized `tfsdk:"tree"`
}

func newConfigResource() resource.Resource { return &configResource{} }

func (r *configResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func pathAttributeValidators() []validator.List {
	return []validator.List{
		listvalidator.SizeAtLeast(1),
		listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1), edgeosValue()),
	}
}

func (r *configResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage any EdgeOS config node by path. Use it for settings that have no dedicated resource.\n\n" +
			"Set exactly one of:\n\n" +
			"- `value` for a leaf with one value (`system time-zone`),\n" +
			"- `values` for a multi-valued leaf (`system name-server`), the order is kept,\n" +
			"- `tree` for a whole subtree as JSON, in the same shape the EdgeOS API uses: objects for nodes, strings for " +
			"values, lists of strings for multi-valued leaves and `null` for valueless nodes,\n" +
			"- none of them to only make sure a valueless node or empty tag exists (`service unms`).\n\n" +
			"With `tree` the resource owns the whole subtree: children added outside Terraform show up as drift and are " +
			"removed on the next apply. Destroying the resource deletes the node.\n\n" +
			"The node must not exist yet when the resource is created; import existing nodes instead. Passwords " +
			"(`plaintext-password`) cannot be managed with `tree`, because EdgeOS replaces them with a hash on commit.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("The path in CLI form, such as `system time-zone`."),
			"path": schema.ListAttribute{
				MarkdownDescription: "Node names from the root, for example `[\"service\", \"dns\", \"forwarding\", \"cache-size\"]`. Changing it creates a new resource.",
				Required:            true,
				ElementType:         types.StringType,
				Validators:          pathAttributeValidators(),
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Value of a single-valued leaf.",
				Optional:            true,
				Validators:          []validator.String{edgeosValue()},
			},
			"values": schema.ListAttribute{
				MarkdownDescription: "Values of a multi-valued leaf, in order.",
				Optional:            true,
				ElementType:         types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(edgeosValue()),
				},
			},
			"tree": schema.StringAttribute{
				MarkdownDescription: "The subtree below `path` as JSON, usually written with `jsonencode()`. Values must be strings, " +
					"lists of strings or `null`; numbers and booleans are rejected because EdgeOS stores everything as text.",
				Optional:   true,
				CustomType: jsontypes.NormalizedType{},
			},
		},
	}
}

func (r *configResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.Conflicting(path.MatchRoot("value"), path.MatchRoot("values"), path.MatchRoot("tree")),
	}
}

func (r *configResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m configModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !known(m.Tree) {
		return
	}
	if _, err := parseTree(m.Tree.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("tree"), "Invalid tree", err.Error())
	}
}

// parseTree decodes and checks a tree attribute.
func parseTree(s string) (map[string]any, error) {
	var x any
	if err := json.Unmarshal([]byte(s), &x); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	m, ok := x.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the tree must be a JSON object; use value or values for leaves")
	}
	if err := checkTree(m, configtree.Path{}); err != nil {
		return nil, err
	}
	if configtree.Flatten(m).Len() == 0 {
		return nil, fmt.Errorf("the tree must contain at least one node")
	}
	return m, nil
}

func checkTree(x any, at configtree.Path) error {
	where := func() string {
		if len(at) == 0 {
			return "the root"
		}
		return fmt.Sprintf("%q", at.String())
	}
	switch t := x.(type) {
	case nil:
		return nil
	case string:
		if err := configtree.ValidateValue(t); err != nil {
			return fmt.Errorf("value at %s: %w", where(), err)
		}
		return nil
	case []any:
		if len(t) == 0 {
			return fmt.Errorf("empty list at %s; remove the node instead", where())
		}
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("list at %s must only contain strings", where())
			}
			if err := configtree.ValidateValue(s); err != nil {
				return fmt.Errorf("value at %s: %w", where(), err)
			}
		}
		return nil
	case map[string]any:
		for k, v := range t {
			if k == "" {
				return fmt.Errorf("empty node name below %s", where())
			}
			if err := configtree.ValidateValue(k); err != nil {
				return fmt.Errorf("node name %q below %s: %w", k, where(), err)
			}
			if err := checkTree(v, at.Child(k)); err != nil {
				return err
			}
		}
		return nil
	}
	kind := "number"
	if _, ok := x.(bool); ok {
		kind = "boolean"
	}
	return fmt.Errorf("value at %s is a %s; EdgeOS values are text, so quote numbers and booleans", where(), kind)
}

func (r *configResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func modelPath(ctx context.Context, m *configModel, diags *diag.Diagnostics) configtree.Path {
	var names []string
	diags.Append(m.Path.ElementsAs(ctx, &names, false)...)
	return configtree.NewPath(names...)
}

// desiredLeaves converts the model into leaves relative to the path.
func (m *configModel) desiredLeaves(ctx context.Context, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	root := configtree.Path{}
	switch {
	case known(m.Value):
		l.SetSingle(root, m.Value.ValueString())
	case known(m.Values):
		var vals []string
		diags.Append(m.Values.ElementsAs(ctx, &vals, false)...)
		l.SetMulti(root, vals...)
	case known(m.Tree):
		t, err := parseTree(m.Tree.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("tree"), "Invalid tree", err.Error())
			return l
		}
		return configtree.Flatten(t)
	default:
		l.SetFlag(root)
	}
	return l
}

// fromDevice updates the model from the node read from the router.
func (m *configModel) fromDevice(x any, infer bool, diags *diag.Diagnostics) {
	val, isLeaf := configtree.ValueFromJSON(x)
	if _, isMap := x.(map[string]any); isMap {
		isLeaf = false
	}
	mode := "flag"
	switch {
	case infer && !isLeaf:
		mode = "tree"
	case infer && val.Kind == configtree.KindSingle:
		mode = "value"
	case infer && val.Kind == configtree.KindMulti:
		mode = "values"
	case !m.Tree.IsNull():
		mode = "tree"
	case !m.Value.IsNull():
		mode = "value"
	case !m.Values.IsNull():
		mode = "values"
	}
	switch mode {
	case "value":
		if !isLeaf || val.Kind == configtree.KindFlag {
			diags.AddError("Node is not a single value", "The node at this path is not a leaf with one value. Use values, tree, or no value at all.")
			return
		}
		m.Value = types.StringValue(val.Values[0])
		if len(val.Values) > 1 {
			// More than one value: report the drift through values.
			m.Value = types.StringValue(fmt.Sprint(val.Values))
		}
	case "values":
		if !isLeaf || val.Kind == configtree.KindFlag {
			diags.AddError("Node is not a multi-valued leaf", "The node at this path is not a leaf with values. Use value, tree, or no value at all.")
			return
		}
		elems := make([]attr.Value, 0, len(val.Values))
		for _, s := range val.Values {
			elems = append(elems, types.StringValue(s))
		}
		m.Values = types.ListValueMust(types.StringType, elems)
	case "tree":
		if isLeaf {
			diags.AddError("Node is a leaf", "The node at this path is a leaf, so it has no tree. Use value or values instead.")
			return
		}
		raw, err := json.Marshal(configtree.Normalize(x))
		if err != nil {
			diags.AddError("Cannot encode tree", err.Error())
			return
		}
		m.Tree = jsontypes.NewNormalizedValue(string(raw))
	}
}

func (r *configResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan configModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := modelPath(ctx, &plan, &resp.Diagnostics)
	desired := plan.desiredLeaves(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	exists, err := r.client.Exists(ctx, p)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", p, err)
		return
	}
	if exists {
		resp.Diagnostics.AddError("Already exists",
			fmt.Sprintf("%q already exists on the router. Import it instead, for example with an import block using id %q.", p.String(), p.String()))
		return
	}
	if err := r.client.Apply(ctx, configtree.Diff(p, configtree.NewLeaves(), desired)); err != nil {
		addClientError(&resp.Diagnostics, "commit", p, err)
		return
	}
	plan.ID = types.StringValue(p.String())
	r.readBack(ctx, p, &plan, false, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *configResource) readBack(ctx context.Context, p configtree.Path, m *configModel, infer bool, diags *diag.Diagnostics) bool {
	x, ok, err := r.client.Read(ctx, p)
	if err != nil {
		addClientError(diags, "read", p, err)
		return false
	}
	if !ok {
		return false
	}
	m.fromDevice(x, infer, diags)
	return true
}

func (r *configResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state configModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	infer := false
	if b, d := req.Private.GetKey(ctx, privateInferKey); d == nil && len(b) > 0 {
		infer = true
	}
	p := modelPath(ctx, &state, &resp.Diagnostics)
	if !r.readBack(ctx, p, &state, infer, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	state.ID = types.StringValue(p.String())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if infer {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateInferKey, nil)...)
	}
}

func (r *configResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state configModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := modelPath(ctx, &plan, &resp.Diagnostics)
	desired := plan.desiredLeaves(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	x, exists, err := r.client.Read(ctx, p)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", p, err)
		return
	}
	b := configtree.NewBatch()
	switch {
	case !exists:
		b = configtree.Diff(p, configtree.NewLeaves(), desired)
	case desired.Has(configtree.Path{}) && desired.Len() == 1:
		// Leaf or flag: replace the node unless it already matches.
		v, _ := desired.Get(configtree.Path{})
		cur, isLeaf := configtree.ValueFromJSON(x)
		if _, isMap := x.(map[string]any); isMap {
			isLeaf = false
		}
		if v.Kind == configtree.KindFlag {
			break
		}
		if !isLeaf || !cur.Equal(v) {
			b.Delete(p)
			b.Set(p, v)
		}
	default:
		cur := configtree.Flatten(x)
		if _, isMap := x.(map[string]any); !isMap {
			b.Delete(p)
			cur = configtree.NewLeaves()
		}
		b.Merge(configtree.Diff(p, cur, desired))
	}
	if err := r.client.Apply(ctx, b); err != nil {
		addClientError(&resp.Diagnostics, "commit", p, err)
		return
	}
	plan.ID = types.StringValue(p.String())
	if !r.readBack(ctx, p, &plan, false, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Node not found after update", fmt.Sprintf("%q disappeared during the update.", p.String()))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *configResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state configModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := modelPath(ctx, &state, &resp.Diagnostics)
	exists, err := r.client.Exists(ctx, p)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", p, err)
		return
	}
	if !exists {
		return
	}
	b := configtree.NewBatch()
	b.Delete(p)
	if err := r.client.Apply(ctx, b); err != nil {
		addClientError(&resp.Diagnostics, "commit", p, err)
	}
}

func (r *configResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p, err := configtree.ParsePath(req.ID)
	if err == nil {
		err = p.Validate()
	}
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Cannot import %q: %s. Use the path in CLI form, for example \"system time-zone\" or 'firewall name WAN_IN'.", req.ID, err))
		return
	}
	elems := make([]attr.Value, 0, len(p))
	for _, n := range p {
		elems = append(elems, types.StringValue(n))
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("path"), types.ListValueMust(types.StringType, elems))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(p.String()))...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateInferKey, []byte(`{"infer":true}`))...)
}
