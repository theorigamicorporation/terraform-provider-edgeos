// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// edgeos_config data source

type configDataSource struct {
	client *client.Client
}

type configDataModel struct {
	ID       types.String         `tfsdk:"id"`
	Path     types.List           `tfsdk:"path"`
	Exists   types.Bool           `tfsdk:"exists"`
	Kind     types.String         `tfsdk:"kind"`
	Value    types.String         `tfsdk:"value"`
	Values   types.List           `tfsdk:"values"`
	Tree     jsontypes.Normalized `tfsdk:"tree"`
	Children types.List           `tfsdk:"children"`
}

func newConfigDataSource() datasource.DataSource { return &configDataSource{} }

func (d *configDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func (d *configDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read any EdgeOS config node by path. A missing node is not an error: `exists` is `false` and the other attributes are null.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The path in CLI form."},
			"path": schema.ListAttribute{
				MarkdownDescription: "Node names from the root, such as `[\"system\", \"host-name\"]`.",
				Required:            true,
				ElementType:         types.StringType,
				Validators:          pathAttributeValidators(),
			},
			"exists": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the node exists."},
			"kind": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`value` (single-valued leaf), `values` (multi-valued leaf), `flag` (valueless node) or `tree` (node with children).",
			},
			"value": schema.StringAttribute{Computed: true, MarkdownDescription: "The value when `kind` is `value`."},
			"values": schema.ListAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "The values when `kind` is `values`.",
			},
			"tree": schema.StringAttribute{
				Computed: true, CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "The subtree as JSON when `kind` is `tree`. Decode it with `jsondecode()`.",
			},
			"children": schema.ListAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Sorted names of the direct children when `kind` is `tree`, handy for listing tag nodes such as firewall rule sets.",
			},
		},
	}
}

func (d *configDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *configDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m configDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var names []string
	resp.Diagnostics.Append(m.Path.ElementsAs(ctx, &names, false)...)
	p := configtree.NewPath(names...)
	x, ok, err := d.client.Read(ctx, p)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", p, err)
		return
	}
	m.ID = types.StringValue(p.String())
	m.Exists = types.BoolValue(ok)
	m.Kind = types.StringNull()
	m.Value = types.StringNull()
	m.Values = types.ListNull(types.StringType)
	m.Tree = jsontypes.NewNormalizedNull()
	m.Children = types.ListNull(types.StringType)
	if ok {
		if _, isMap := x.(map[string]any); isMap {
			raw, err := json.Marshal(configtree.Normalize(x))
			if err != nil {
				resp.Diagnostics.AddError("Cannot encode tree", err.Error())
				return
			}
			m.Kind = types.StringValue("tree")
			m.Tree = jsontypes.NewNormalizedValue(string(raw))
			m.Children = stringList(configtree.Children(x))
		} else {
			v, _ := configtree.ValueFromJSON(x)
			switch v.Kind {
			case configtree.KindFlag:
				m.Kind = types.StringValue("flag")
			case configtree.KindSingle:
				m.Kind = types.StringValue("value")
				m.Value = types.StringValue(v.String())
			case configtree.KindMulti:
				m.Kind = types.StringValue("values")
				m.Values = stringList(v.Values)
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func stringList(vals []string) types.List {
	elems := make([]attr.Value, 0, len(vals))
	for _, s := range vals {
		elems = append(elems, types.StringValue(s))
	}
	return types.ListValueMust(types.StringType, elems)
}

// edgeos_system_info data source

type systemInfoDataSource struct {
	client *client.Client
}

type systemInfoModel struct {
	ID              types.String `tfsdk:"id"`
	SoftwareVersion types.String `tfsdk:"software_version"`
	Version         types.String `tfsdk:"version"`
	Build           types.String `tfsdk:"build"`
	Platform        types.String `tfsdk:"platform"`
	Models          types.String `tfsdk:"models"`
	HostName        types.String `tfsdk:"host_name"`
	DomainName      types.String `tfsdk:"domain_name"`
}

func newSystemInfoDataSource() datasource.DataSource { return &systemInfoDataSource{} }

func (d *systemInfoDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_info"
}

func (d *systemInfoDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Firmware and identity of the router. EdgeOS does not report the exact model over the API, only the " +
			"hardware platform, which several models share.",
		Attributes: map[string]schema.Attribute{
			"id":               computed("Same as `software_version`."),
			"software_version": computed("Full version string, such as `EdgeRouter.ER-e50.v3.0.1.5862409.250924.1407`."),
			"version":          computed("Firmware release, such as `v3.0.1`."),
			"build":            computed("Build identifier after the release."),
			"platform":         computed("Hardware platform code, such as `e50`."),
			"models":           computed("Models that use this platform, such as `EdgeRouter X, X SFP, 10X`. Empty for unknown platforms."),
			"host_name":        computed("`system host-name`."),
			"domain_name":      computed("`system domain-name`, null if not set."),
		},
	}
}

func (d *systemInfoDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *systemInfoDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.SystemInfo(ctx)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", configtree.NewPath("sys_info"), err)
		return
	}
	sys, _, err := d.client.Read(ctx, configtree.NewPath("system"))
	if err != nil {
		addClientError(&resp.Diagnostics, "read", configtree.NewPath("system"), err)
		return
	}
	m := systemInfoModel{
		ID:              types.StringValue(info.SoftwareVersion),
		SoftwareVersion: types.StringValue(info.SoftwareVersion),
		Version:         types.StringValue(info.Version),
		Build:           types.StringValue(info.Build),
		Platform:        types.StringValue(info.Platform),
		Models:          types.StringValue(client.PlatformModels[info.Platform]),
		HostName:        getString(sys, "host-name"),
		DomainName:      getString(sys, "domain-name"),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
