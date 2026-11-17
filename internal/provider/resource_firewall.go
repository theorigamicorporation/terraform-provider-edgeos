// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Shared attribute builders.

func idAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func descriptionAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Free text description.",
		Optional:            true,
		Validators:          []validator.String{edgeosValue(), stringvalidator.LengthBetween(1, 255)},
	}
}

func disabledAttribute(what string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: fmt.Sprintf("Keep the %s in the config but switch it off (the `disable` node). Defaults to `false`.", what),
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
	}
}

// firewall name

type firewallNameModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	DefaultAction    types.String `tfsdk:"default_action"`
	EnableDefaultLog types.Bool   `tfsdk:"enable_default_log"`
}

func newFirewallNameResource() resourceFactory {
	return newLeafResource(&leafSpec[firewallNameModel]{
		Name: "firewall_name",
		Schema: schema.Schema{
			MarkdownDescription: "An IPv4 firewall rule set (`firewall name <name>`). Rules are managed with " +
				"`edgeos_firewall_rule`; this resource only owns the rule set's own settings and never touches its rules on update.\n\n" +
				"Destroying the rule set deletes the whole node, including any rules that Terraform does not manage. " +
				"EdgeOS refuses to delete a rule set that an interface still references, so detach it first.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The rule set name."),
				"name": schema.StringAttribute{
					MarkdownDescription: "Rule set name, such as `WAN_IN`. Changing it creates a new rule set.",
					Required:            true,
					Validators:          []validator.String{firewallName()},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"description": descriptionAttribute(),
				"default_action": schema.StringAttribute{
					MarkdownDescription: "What happens to packets no rule matches: `drop`, `reject` or `accept`. Defaults to `drop`.",
					Optional:            true,
					Computed:            true,
					Default:             stringdefault.StaticString("drop"),
					Validators:          []validator.String{stringvalidator.OneOf("drop", "reject", "accept")},
				},
				"enable_default_log": schema.BoolAttribute{
					MarkdownDescription: "Log packets handled by the default action. Defaults to `false`.",
					Optional:            true,
					Computed:            true,
					Default:             booldefault.StaticBool(false),
				},
			},
		},
		Path: func(m *firewallNameModel) configtree.Path {
			return configtree.NewPath("firewall", "name", m.Name.ValueString())
		},
		ID:    func(m *firewallNameModel) string { return m.Name.ValueString() },
		SetID: func(m *firewallNameModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(_ context.Context, m *firewallNameModel, _ *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putString(l, m.Description, "description")
			putString(l, m.DefaultAction, "default-action")
			putFlag(l, m.EnableDefaultLog, "enable-default-log")
			return l
		},
		FromTree: func(_ context.Context, tree any, m *firewallNameModel, _ *diag.Diagnostics) {
			m.Description = getString(tree, "description")
			m.DefaultAction = getString(tree, "default-action")
			if m.DefaultAction.IsNull() {
				// EdgeOS treats a missing default-action as drop.
				m.DefaultAction = types.StringValue("drop")
			}
			m.EnableDefaultLog = getFlag(tree, "enable-default-log")
		},
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if !firewallNameRE.MatchString(id) {
				return nil, fmt.Errorf("%q is not a valid rule set name", id)
			}
			return map[string]attr.Value{"name": types.StringValue(id)}, nil
		},
		ImportHint: "the rule set name, for example WAN_IN",
	})
}

// firewall rule

type firewallRuleModel struct {
	ID          types.String  `tfsdk:"id"`
	Ruleset     types.String  `tfsdk:"ruleset"`
	Number      types.Int64   `tfsdk:"number"`
	Action      types.String  `tfsdk:"action"`
	Description types.String  `tfsdk:"description"`
	Protocol    types.String  `tfsdk:"protocol"`
	Log         types.Bool    `tfsdk:"log"`
	Disabled    types.Bool    `tfsdk:"disabled"`
	State       *fwStateModel `tfsdk:"state"`
	Source      *fwSource     `tfsdk:"source"`
	Destination *fwDest       `tfsdk:"destination"`
	ICMP        *fwICMP       `tfsdk:"icmp"`
	TCPFlags    types.String  `tfsdk:"tcp_flags"`
}

type fwStateModel struct {
	Established types.Bool `tfsdk:"established"`
	Related     types.Bool `tfsdk:"related"`
	New         types.Bool `tfsdk:"new"`
	Invalid     types.Bool `tfsdk:"invalid"`
}

type fwSource struct {
	Address      types.String `tfsdk:"address"`
	Port         types.String `tfsdk:"port"`
	MACAddress   types.String `tfsdk:"mac_address"`
	AddressGroup types.String `tfsdk:"address_group"`
	NetworkGroup types.String `tfsdk:"network_group"`
	PortGroup    types.String `tfsdk:"port_group"`
}

type fwDest struct {
	Address      types.String `tfsdk:"address"`
	Port         types.String `tfsdk:"port"`
	AddressGroup types.String `tfsdk:"address_group"`
	NetworkGroup types.String `tfsdk:"network_group"`
	PortGroup    types.String `tfsdk:"port_group"`
}

type fwICMP struct {
	TypeName types.String `tfsdk:"type_name"`
	Type     types.Int64  `tfsdk:"type"`
	Code     types.Int64  `tfsdk:"code"`
}

func matchAttributes(withMAC bool) map[string]schema.Attribute {
	group := func(kind string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: fmt.Sprintf("Name of a firewall %s group to match.", kind),
			Optional:            true,
			Validators:          []validator.String{tagName()},
		}
	}
	attrs := map[string]schema.Attribute{
		"address": schema.StringAttribute{
			MarkdownDescription: "Address, network (`192.0.2.0/24`) or range (`192.0.2.10-192.0.2.20`). Prefix with `!` to negate.",
			Optional:            true,
			Validators:          []validator.String{edgeosValue()},
		},
		"port": schema.StringAttribute{
			MarkdownDescription: "Port, range, service name or comma separated list, such as `22`, `8000-8080` or `http,https`. Needs `protocol` `tcp`, `udp` or `tcp_udp`.",
			Optional:            true,
			Validators:          []validator.String{portSpec()},
		},
		"address_group": group("address"),
		"network_group": group("network"),
		"port_group":    group("port"),
	}
	if withMAC {
		attrs["mac_address"] = schema.StringAttribute{
			MarkdownDescription: "Source MAC address in lower case.",
			Optional:            true,
			Validators:          []validator.String{macAddress()},
		}
	}
	return attrs
}

func stateBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: desc, Optional: true}
}

func newFirewallRuleResource() resourceFactory {
	return newLeafResource(&leafSpec[firewallRuleModel]{
		Name: "firewall_rule",
		Schema: schema.Schema{
			MarkdownDescription: "One rule in an IPv4 firewall rule set (`firewall name <ruleset> rule <number>`). " +
				"The resource owns the attributes below; other options of the rule (for example `time` or `recent`) are left " +
				"untouched, use `edgeos_config` for those.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("`<ruleset>:<number>`."),
				"ruleset": schema.StringAttribute{
					MarkdownDescription: "Name of the rule set, usually `edgeos_firewall_name.x.name`.",
					Required:            true,
					Validators:          []validator.String{firewallName()},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"number": schema.Int64Attribute{
					MarkdownDescription: "Rule number from 1 to 9999. Rules are evaluated in ascending order.",
					Required:            true,
					Validators:          []validator.Int64{int64validator.Between(1, 9999)},
					PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				},
				"action": schema.StringAttribute{
					MarkdownDescription: "`accept`, `drop`, `reject` or `reject-tcp` (needs `protocol = \"tcp\"`).",
					Required:            true,
					Validators:          []validator.String{stringvalidator.OneOf("accept", "drop", "reject", "reject-tcp")},
				},
				"description": descriptionAttribute(),
				"protocol": schema.StringAttribute{
					MarkdownDescription: "Protocol name or number, such as `tcp`, `udp`, `tcp_udp`, `icmp` or `all`. Prefix with `!` to negate.",
					Optional:            true,
					Validators:          []validator.String{stringvalidator.RegexMatches(protocolRE, "must be a protocol name or number such as tcp, tcp_udp or 47")},
				},
				"log": schema.BoolAttribute{
					MarkdownDescription: "Log packets matching this rule. Unset leaves the EdgeOS default (no logging).",
					Optional:            true,
				},
				"disabled": disabledAttribute("rule"),
				"state": schema.SingleNestedAttribute{
					MarkdownDescription: "Match on connection tracking state. Each attribute set to `true` or `false` writes `enable` or `disable`.",
					Optional:            true,
					Attributes: map[string]schema.Attribute{
						"established": stateBool("Match established connections."),
						"related":     stateBool("Match related connections."),
						"new":         stateBool("Match new connections."),
						"invalid":     stateBool("Match invalid packets."),
					},
				},
				"source": schema.SingleNestedAttribute{
					MarkdownDescription: "Match on the source of the packet.",
					Optional:            true,
					Attributes:          matchAttributes(true),
				},
				"destination": schema.SingleNestedAttribute{
					MarkdownDescription: "Match on the destination of the packet.",
					Optional:            true,
					Attributes:          matchAttributes(false),
				},
				"icmp": schema.SingleNestedAttribute{
					MarkdownDescription: "Match ICMP type and code. Needs `protocol = \"icmp\"`.",
					Optional:            true,
					Attributes: map[string]schema.Attribute{
						"type_name": schema.StringAttribute{
							MarkdownDescription: "ICMP type name such as `echo-request`. Conflicts with `type`.",
							Optional:            true,
							Validators:          []validator.String{tagName()},
						},
						"type": schema.Int64Attribute{
							MarkdownDescription: "ICMP type number.",
							Optional:            true,
							Validators:          []validator.Int64{int64validator.Between(0, 255)},
						},
						"code": schema.Int64Attribute{
							MarkdownDescription: "ICMP code number, used together with `type`.",
							Optional:            true,
							Validators:          []validator.Int64{int64validator.Between(0, 255), int64validator.AlsoRequires(path.MatchRelative().AtParent().AtName("type"))},
						},
					},
				},
				"tcp_flags": schema.StringAttribute{
					MarkdownDescription: "TCP flags to match, such as `SYN,!ACK`. Needs `protocol = \"tcp\"`.",
					Optional:            true,
					Validators:          []validator.String{edgeosValue()},
				},
			},
		},
		Path: func(m *firewallRuleModel) configtree.Path {
			return configtree.NewPath("firewall", "name", m.Ruleset.ValueString(), "rule", strconv.FormatInt(m.Number.ValueInt64(), 10))
		},
		ID: func(m *firewallRuleModel) string {
			return fmt.Sprintf("%s:%d", m.Ruleset.ValueString(), m.Number.ValueInt64())
		},
		SetID:       func(m *firewallRuleModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    firewallRuleToLeaves,
		FromTree:    firewallRuleFromTree,
		ImportAttrs: rulesetNumberImport,
		ImportHint:  "<ruleset>:<number>, for example WAN_IN:10",
		Validate:    validateFirewallRule,
	})
}

func rulesetNumberImport(id string) (map[string]attr.Value, error) {
	name, num, ok := strings.Cut(id, ":")
	if !ok {
		return nil, fmt.Errorf("missing ':'")
	}
	if !firewallNameRE.MatchString(name) {
		return nil, fmt.Errorf("%q is not a valid rule set name", name)
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 1 || n > 9999 {
		return nil, fmt.Errorf("%q is not a rule number from 1 to 9999", num)
	}
	return map[string]attr.Value{"ruleset": types.StringValue(name), "number": types.Int64Value(n)}, nil
}

func firewallRuleToLeaves(_ context.Context, m *firewallRuleModel, _ *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putString(l, m.Action, "action")
	putString(l, m.Description, "description")
	putString(l, m.Protocol, "protocol")
	putEnable(l, m.Log, "log")
	putFlag(l, m.Disabled, "disable")
	if s := m.State; s != nil {
		putEnable(l, s.Established, "state", "established")
		putEnable(l, s.Related, "state", "related")
		putEnable(l, s.New, "state", "new")
		putEnable(l, s.Invalid, "state", "invalid")
	}
	if s := m.Source; s != nil {
		putString(l, s.Address, "source", "address")
		putString(l, s.Port, "source", "port")
		putString(l, s.MACAddress, "source", "mac-address")
		putString(l, s.AddressGroup, "source", "group", "address-group")
		putString(l, s.NetworkGroup, "source", "group", "network-group")
		putString(l, s.PortGroup, "source", "group", "port-group")
	}
	if d := m.Destination; d != nil {
		putString(l, d.Address, "destination", "address")
		putString(l, d.Port, "destination", "port")
		putString(l, d.AddressGroup, "destination", "group", "address-group")
		putString(l, d.NetworkGroup, "destination", "group", "network-group")
		putString(l, d.PortGroup, "destination", "group", "port-group")
	}
	if i := m.ICMP; i != nil {
		putString(l, i.TypeName, "icmp", "type-name")
		putInt(l, i.Type, "icmp", "type")
		putInt(l, i.Code, "icmp", "code")
	}
	putString(l, m.TCPFlags, "tcp", "flags")
	return l
}

func firewallRuleFromTree(_ context.Context, tree any, m *firewallRuleModel, diags *diag.Diagnostics) {
	m.Action = getString(tree, "action")
	m.Description = getString(tree, "description")
	m.Protocol = getString(tree, "protocol")
	m.Log = getEnable(tree, "log")
	m.Disabled = getFlag(tree, "disable")
	m.TCPFlags = getString(tree, "tcp", "flags")

	m.State = nil
	if st, ok := lookup(tree, "state"); ok && hasAny(st, "established", "related", "new", "invalid") {
		m.State = &fwStateModel{
			Established: getEnable(st, "established"),
			Related:     getEnable(st, "related"),
			New:         getEnable(st, "new"),
			Invalid:     getEnable(st, "invalid"),
		}
	}
	m.Source = nil
	if src, ok := lookup(tree, "source"); ok {
		s := &fwSource{
			Address:      getString(src, "address"),
			Port:         getString(src, "port"),
			MACAddress:   getString(src, "mac-address"),
			AddressGroup: getString(src, "group", "address-group"),
			NetworkGroup: getString(src, "group", "network-group"),
			PortGroup:    getString(src, "group", "port-group"),
		}
		if !allNull(s.Address, s.Port, s.MACAddress, s.AddressGroup, s.NetworkGroup, s.PortGroup) {
			m.Source = s
		}
	}
	m.Destination = nil
	if dst, ok := lookup(tree, "destination"); ok {
		d := &fwDest{
			Address:      getString(dst, "address"),
			Port:         getString(dst, "port"),
			AddressGroup: getString(dst, "group", "address-group"),
			NetworkGroup: getString(dst, "group", "network-group"),
			PortGroup:    getString(dst, "group", "port-group"),
		}
		if !allNull(d.Address, d.Port, d.AddressGroup, d.NetworkGroup, d.PortGroup) {
			m.Destination = d
		}
	}
	m.ICMP = nil
	if ic, ok := lookup(tree, "icmp"); ok {
		i := &fwICMP{
			TypeName: getString(ic, "type-name"),
			Type:     getInt(ic, diags, "type"),
			Code:     getInt(ic, diags, "code"),
		}
		if !allNull(i.TypeName, i.Type, i.Code) {
			m.ICMP = i
		}
	}
}

func allNull(vals ...attr.Value) bool {
	for _, v := range vals {
		if !v.IsNull() {
			return false
		}
	}
	return true
}

func validateFirewallRule(_ context.Context, m *firewallRuleModel, diags *diag.Diagnostics) {
	proto := strings.TrimPrefix(m.Protocol.ValueString(), "!")
	protoKnown := !m.Protocol.IsUnknown()
	portsAllowed := proto == "tcp" || proto == "udp" || proto == "tcp_udp"
	if known(m.Action) && m.Action.ValueString() == "reject-tcp" && protoKnown && proto != "tcp" {
		diags.AddAttributeError(path.Root("action"), "reject-tcp needs protocol tcp",
			"EdgeOS only allows the reject-tcp action on rules with protocol = \"tcp\".")
	}
	if protoKnown {
		if m.Source != nil && known(m.Source.Port) && !portsAllowed {
			diags.AddAttributeError(path.Root("source").AtName("port"), "Port needs tcp or udp",
				"Set protocol to tcp, udp or tcp_udp to match on ports.")
		}
		if m.Destination != nil && known(m.Destination.Port) && !portsAllowed {
			diags.AddAttributeError(path.Root("destination").AtName("port"), "Port needs tcp or udp",
				"Set protocol to tcp, udp or tcp_udp to match on ports.")
		}
		if m.ICMP != nil && proto != "icmp" {
			diags.AddAttributeError(path.Root("icmp"), "ICMP match needs protocol icmp", "Set protocol = \"icmp\".")
		}
		if known(m.TCPFlags) && proto != "tcp" {
			diags.AddAttributeError(path.Root("tcp_flags"), "TCP flags need protocol tcp", "Set protocol = \"tcp\".")
		}
	}
	if m.State != nil && allNull(m.State.Established, m.State.Related, m.State.New, m.State.Invalid) {
		diags.AddAttributeError(path.Root("state"), "Empty state block", "Set at least one state, or remove the block.")
	}
	if m.Source != nil && allNull(m.Source.Address, m.Source.Port, m.Source.MACAddress, m.Source.AddressGroup, m.Source.NetworkGroup, m.Source.PortGroup) {
		diags.AddAttributeError(path.Root("source"), "Empty source block", "Set at least one attribute, or remove the block.")
	}
	if m.Destination != nil && allNull(m.Destination.Address, m.Destination.Port, m.Destination.AddressGroup, m.Destination.NetworkGroup, m.Destination.PortGroup) {
		diags.AddAttributeError(path.Root("destination"), "Empty destination block", "Set at least one attribute, or remove the block.")
	}
	if m.ICMP != nil && allNull(m.ICMP.TypeName, m.ICMP.Type, m.ICMP.Code) {
		diags.AddAttributeError(path.Root("icmp"), "Empty icmp block", "Set type_name or type, or remove the block.")
	}
	if m.ICMP != nil && known(m.ICMP.TypeName) && known(m.ICMP.Type) {
		diags.AddAttributeError(path.Root("icmp"), "Conflicting ICMP match", "Set either type_name or type, not both.")
	}
	if m.Source != nil && known(m.Source.Address) && known(m.Source.AddressGroup) {
		diags.AddAttributeError(path.Root("source"), "Conflicting source match", "Set either address or address_group, not both.")
	}
	if m.Destination != nil && known(m.Destination.Address) && known(m.Destination.AddressGroup) {
		diags.AddAttributeError(path.Root("destination"), "Conflicting destination match", "Set either address or address_group, not both.")
	}
}
