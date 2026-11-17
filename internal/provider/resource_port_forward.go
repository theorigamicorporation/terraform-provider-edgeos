// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	portListRE  = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*$`)
	portRangeRE = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?$`)
)

const portForwardMaxRule = 4294967295

// port-forward rule

type portForwardModel struct {
	ID           types.String `tfsdk:"id"`
	Number       types.Int64  `tfsdk:"number"`
	Description  types.String `tfsdk:"description"`
	OriginalPort types.String `tfsdk:"original_port"`
	Protocol     types.String `tfsdk:"protocol"`
	ForwardTo    *pfForwardTo `tfsdk:"forward_to"`
}

type pfForwardTo struct {
	Address types.String `tfsdk:"address"`
	Port    types.String `tfsdk:"port"`
}

func newPortForwardResource() resourceFactory {
	return newLeafResource(&leafSpec[portForwardModel]{
		Name: "port_forward",
		Schema: schema.Schema{
			MarkdownDescription: "One port forwarding rule (`port-forward rule <number>`), the rules shown on the " +
				"Firewall/NAT > Port Forwarding page of the web UI.\n\n" +
				"EdgeOS only commits port forwarding rules when `wan-interface` and `lan-interface` are set, so manage those " +
				"with `edgeos_port_forward_settings` and add `depends_on` pointing at it. Terraform then creates the settings " +
				"first and destroys them last.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The rule number."),
				"number": schema.Int64Attribute{
					MarkdownDescription: "Rule number, 1 or higher. Changing it creates a new rule.",
					Required:            true,
					Validators:          []validator.Int64{int64validator.Between(1, portForwardMaxRule)},
					PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				},
				"description": descriptionAttribute(),
				"original_port": schema.StringAttribute{
					MarkdownDescription: "Port on the WAN interface: a number, a range or a comma separated list, such as `443`, " +
						"`8000-8010` or `80,443`.",
					Required:   true,
					Validators: []validator.String{stringvalidator.RegexMatches(portListRE, "must be a port, a range such as 8000-8010, or a comma separated list of those")},
				},
				"protocol": schema.StringAttribute{
					MarkdownDescription: "`tcp`, `udp` or `tcp_udp`. Defaults to `tcp_udp`.",
					Optional:            true,
					Computed:            true,
					Default:             stringdefault.StaticString("tcp_udp"),
					Validators:          []validator.String{stringvalidator.OneOf("tcp", "udp", "tcp_udp")},
				},
				"forward_to": schema.SingleNestedAttribute{
					MarkdownDescription: "The LAN host the traffic goes to.",
					Required:            true,
					Attributes: map[string]schema.Attribute{
						"address": schema.StringAttribute{
							MarkdownDescription: "IPv4 address of the LAN host, such as `192.168.1.20`.",
							Required:            true,
							Validators:          []validator.String{ipv4Address()},
						},
						"port": schema.StringAttribute{
							MarkdownDescription: "Port or range on the LAN host. Unset keeps `original_port`.",
							Optional:            true,
							Validators:          []validator.String{stringvalidator.RegexMatches(portRangeRE, "must be a port or a range such as 8000-8010")},
						},
					},
				},
			},
		},
		Path: func(m *portForwardModel) configtree.Path {
			return configtree.NewPath("port-forward", "rule", strconv.FormatInt(m.Number.ValueInt64(), 10))
		},
		ID:    func(m *portForwardModel) string { return strconv.FormatInt(m.Number.ValueInt64(), 10) },
		SetID: func(m *portForwardModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(_ context.Context, m *portForwardModel, _ *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putString(l, m.Description, "description")
			putString(l, m.OriginalPort, "original-port")
			putString(l, m.Protocol, "protocol")
			if f := m.ForwardTo; f != nil {
				putString(l, f.Address, "forward-to", "address")
				putString(l, f.Port, "forward-to", "port")
			}
			return l
		},
		FromTree: func(_ context.Context, tree any, m *portForwardModel, _ *diag.Diagnostics) {
			m.Description = getString(tree, "description")
			m.OriginalPort = getString(tree, "original-port")
			m.Protocol = getString(tree, "protocol")
			m.ForwardTo = nil
			if ft, ok := lookup(tree, "forward-to"); ok {
				f := &pfForwardTo{Address: getString(ft, "address"), Port: getString(ft, "port")}
				if !allNull(f.Address, f.Port) {
					m.ForwardTo = f
				}
			}
		},
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			return numberImport(id, "number", 1, portForwardMaxRule)
		},
		ImportHint: "the rule number, for example 1",
	})
}

// port-forward settings

type portForwardSettingsModel struct {
	ID            types.String `tfsdk:"id"`
	AutoFirewall  types.Bool   `tfsdk:"auto_firewall"`
	HairpinNAT    types.Bool   `tfsdk:"hairpin_nat"`
	WANInterface  types.String `tfsdk:"wan_interface"`
	LANInterfaces types.Set    `tfsdk:"lan_interfaces"`
}

func (m *portForwardSettingsModel) allNull() bool {
	return allNull(m.AutoFirewall, m.HairpinNAT, m.WANInterface, m.LANInterfaces)
}

const portForwardSettingsID = "port-forward"

func newPortForwardSettingsResource() resourceFactory {
	return newLeafResource(&leafSpec[portForwardSettingsModel]{
		Name: "port_forward_settings",
		Schema: schema.Schema{
			MarkdownDescription: "The global port forwarding options (`port-forward`). There is only one of these per " +
				"router, so the resource takes over the existing node on create and the ID is always `port-forward`.\n\n" +
				"The resource only manages the attributes you set. Attributes left unset are not read or changed, and " +
				"destroying the resource removes only the attributes it managed. Port forwarding rules below " +
				"`port-forward rule` are never touched; manage them with `edgeos_port_forward`.\n\n" +
				"EdgeOS refuses to commit port forwarding rules without `wan_interface` and `lan_interfaces`, so set both " +
				"as soon as any rule exists, and do not destroy them while rules remain.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("Always `port-forward`."),
				"auto_firewall": schema.BoolAttribute{
					MarkdownDescription: "Let EdgeOS add the firewall rules that allow the forwarded traffic in " +
						"(`auto-firewall enable`). Unset leaves the current setting alone.",
					Optional: true,
				},
				"hairpin_nat": schema.BoolAttribute{
					MarkdownDescription: "Make forwarded ports reachable from the LAN through the WAN address " +
						"(`hairpin-nat enable`). Unset leaves the current setting alone.",
					Optional: true,
				},
				"wan_interface": schema.StringAttribute{
					MarkdownDescription: "Interface the forwarded ports are opened on, such as `eth0` or `pppoe0`.",
					Optional:            true,
					Validators:          []validator.String{interfaceName()},
				},
				"lan_interfaces": schema.SetAttribute{
					MarkdownDescription: "Interfaces the LAN hosts live on, used for hairpin NAT, such as `[\"switch0\"]`.",
					Optional:            true,
					ElementType:         types.StringType,
					Validators: []validator.Set{
						setvalidator.SizeAtLeast(1),
						setvalidator.ValueStringsAre(interfaceName()),
					},
				},
			},
		},
		Path:  func(*portForwardSettingsModel) configtree.Path { return configtree.NewPath("port-forward") },
		ID:    func(*portForwardSettingsModel) string { return portForwardSettingsID },
		SetID: func(m *portForwardSettingsModel, id string) { m.ID = types.StringValue(id) },
		Adopt: true,
		ToLeaves: func(ctx context.Context, m *portForwardSettingsModel, diags *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putEnable(l, m.AutoFirewall, "auto-firewall")
			putEnable(l, m.HairpinNAT, "hairpin-nat")
			putString(l, m.WANInterface, "wan-interface")
			putStrings(ctx, l, m.LANInterfaces, diags, "lan-interface")
			return l
		},
		FromTree: portForwardSettingsFromTree,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if id != portForwardSettingsID {
				return nil, fmt.Errorf("there is only one port-forward node")
			}
			return map[string]attr.Value{"id": types.StringValue(portForwardSettingsID)}, nil
		},
		ImportHint: "the literal ID port-forward",
		Validate: func(_ context.Context, m *portForwardSettingsModel, diags *diag.Diagnostics) {
			if m.allNull() {
				diags.AddError("Nothing to manage",
					"Set at least one of auto_firewall, hairpin_nat, wan_interface or lan_interfaces.")
			}
			if known(m.WANInterface) && known(m.LANInterfaces) {
				for _, e := range m.LANInterfaces.Elements() {
					if s, ok := e.(types.String); ok && s.Equal(m.WANInterface) {
						diags.AddAttributeError(path.Root("lan_interfaces"), "WAN interface listed as LAN",
							fmt.Sprintf("%s is the wan_interface and cannot also be a LAN interface.", s.ValueString()))
					}
				}
			}
		},
	})
}

// portForwardSettingsFromTree refreshes only the attributes the resource
// manages, so options set elsewhere do not show up as changes to remove.
// A model with no managed attribute only exists right after import (the
// config validation needs at least one), and then everything is read.
func portForwardSettingsFromTree(_ context.Context, tree any, m *portForwardSettingsModel, _ *diag.Diagnostics) {
	all := m.allNull()
	if all || !m.AutoFirewall.IsNull() {
		m.AutoFirewall = getEnable(tree, "auto-firewall")
	}
	if all || !m.HairpinNAT.IsNull() {
		m.HairpinNAT = getEnable(tree, "hairpin-nat")
	}
	if all || !m.WANInterface.IsNull() {
		m.WANInterface = getString(tree, "wan-interface")
	}
	if all || !m.LANInterfaces.IsNull() {
		m.LANInterfaces = getSet(tree, "lan-interface")
	}
}
