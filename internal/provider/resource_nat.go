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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// service nat rule

type natRuleModel struct {
	ID                types.String `tfsdk:"id"`
	Number            types.Int64  `tfsdk:"number"`
	Type              types.String `tfsdk:"type"`
	Description       types.String `tfsdk:"description"`
	InboundInterface  types.String `tfsdk:"inbound_interface"`
	OutboundInterface types.String `tfsdk:"outbound_interface"`
	Protocol          types.String `tfsdk:"protocol"`
	Log               types.Bool   `tfsdk:"log"`
	Disabled          types.Bool   `tfsdk:"disabled"`
	Exclude           types.Bool   `tfsdk:"exclude"`
	Source            *fwDest      `tfsdk:"source"`
	Destination       *fwDest      `tfsdk:"destination"`
	InsideAddress     *natAddress  `tfsdk:"inside_address"`
	OutsideAddress    *natAddress  `tfsdk:"outside_address"`
}

type natAddress struct {
	Address types.String `tfsdk:"address"`
	Port    types.String `tfsdk:"port"`
}

func natAddressAttribute(desc, addrDesc string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"address": schema.StringAttribute{
				MarkdownDescription: addrDesc,
				Required:            true,
				Validators:          []validator.String{edgeosValue()},
			},
			"port": schema.StringAttribute{
				MarkdownDescription: "Port or port range to translate to, such as `443` or `8000-8010`. Needs `protocol` `tcp`, `udp` or `tcp_udp`.",
				Optional:            true,
				Validators:          []validator.String{portSpec()},
			},
		},
	}
}

func newNATRuleResource() resourceFactory {
	return newLeafResource(&leafSpec[natRuleModel]{
		Name: "nat_rule",
		Schema: schema.Schema{
			MarkdownDescription: "One NAT rule (`service nat rule <number>`).\n\n" +
				"There are three kinds of rule:\n\n" +
				"- `destination` rewrites the destination of packets arriving on `inbound_interface` to `inside_address` (DNAT, " +
				"what a port forward does).\n" +
				"- `source` rewrites the source of packets leaving through `outbound_interface` to `outside_address` (SNAT).\n" +
				"- `masquerade` rewrites the source to the address of `outbound_interface`, the usual rule for a WAN link with a " +
				"dynamic address.\n\n" +
				"Rules are evaluated in ascending order. The EdgeOS web UI keeps destination rules in 1 to 4999 and source and " +
				"masquerade rules in 5000 to 9999; the provider warns when a rule is outside its range because the UI then " +
				"shows it in the wrong list. Rules created by `port-forward` are separate and are not visible here.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The rule number."),
				"number": schema.Int64Attribute{
					MarkdownDescription: "Rule number from 1 to 9999. Changing it creates a new rule.",
					Required:            true,
					Validators:          []validator.Int64{int64validator.Between(1, 9999)},
					PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				},
				"type": schema.StringAttribute{
					MarkdownDescription: "`destination`, `source` or `masquerade`.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.OneOf("destination", "source", "masquerade")},
				},
				"description": descriptionAttribute(),
				"inbound_interface": schema.StringAttribute{
					MarkdownDescription: "Interface the packets arrive on, such as `eth0` or `pppoe0`. Required for `destination` rules " +
						"and not allowed for the other types.",
					Optional:   true,
					Validators: []validator.String{interfaceName()},
				},
				"outbound_interface": schema.StringAttribute{
					MarkdownDescription: "Interface the packets leave through. Required for `source` and `masquerade` rules and not " +
						"allowed for `destination` rules.",
					Optional:   true,
					Validators: []validator.String{interfaceName()},
				},
				"protocol": schema.StringAttribute{
					MarkdownDescription: "Protocol to match, such as `tcp`, `udp`, `tcp_udp` or `all`. Unset matches all protocols. " +
						"Matching or translating ports needs `tcp`, `udp` or `tcp_udp`.",
					Optional:   true,
					Validators: []validator.String{stringvalidator.RegexMatches(protocolRE, "must be a protocol name or number such as tcp, tcp_udp or 47")},
				},
				"log": schema.BoolAttribute{
					MarkdownDescription: "Log packets matching this rule. Unset leaves the EdgeOS default (no logging).",
					Optional:            true,
				},
				"disabled": disabledAttribute("rule"),
				"exclude": schema.BoolAttribute{
					MarkdownDescription: "Exclude matching packets from NAT (the `exclude` node). Later rules are not evaluated for " +
						"them. An exclude rule does not need a translation address. Defaults to `false`.",
					Optional: true,
					Computed: true,
					Default:  booldefault.StaticBool(false),
				},
				"source": schema.SingleNestedAttribute{
					MarkdownDescription: "Match on the source of the packet.",
					Optional:            true,
					Attributes:          matchAttributes(false),
				},
				"destination": schema.SingleNestedAttribute{
					MarkdownDescription: "Match on the destination of the packet. For a `destination` rule this is usually the WAN " +
						"address and the public port.",
					Optional:   true,
					Attributes: matchAttributes(false),
				},
				"inside_address": natAddressAttribute(
					"Where a `destination` rule sends the packets. Required for `destination` rules unless `exclude` is set, "+
						"not allowed for the other types.",
					"Inside address, network or range, such as `192.168.1.10`."),
				"outside_address": natAddressAttribute(
					"What a `source` rule rewrites the source to. Required for `source` rules unless `exclude` is set, not "+
						"allowed for `destination` and `masquerade` rules.",
					"Outside address, network or range, such as `203.0.113.10` or `203.0.113.8/29`."),
			},
		},
		Path: func(m *natRuleModel) configtree.Path {
			return configtree.NewPath("service", "nat", "rule", strconv.FormatInt(m.Number.ValueInt64(), 10))
		},
		ID:          func(m *natRuleModel) string { return strconv.FormatInt(m.Number.ValueInt64(), 10) },
		SetID:       func(m *natRuleModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    natRuleToLeaves,
		FromTree:    natRuleFromTree,
		ImportAttrs: natRuleImport,
		ImportHint:  "the rule number, for example 5010",
		Validate:    validateNATRule,
	})
}

// numberImport parses a rule number between lo and hi into attribute name.
func numberImport(id, name string, lo, hi int64) (map[string]attr.Value, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < lo || n > hi || strconv.FormatInt(n, 10) != id {
		return nil, fmt.Errorf("%q is not a rule number from %d to %d", id, lo, hi)
	}
	return map[string]attr.Value{name: types.Int64Value(n)}, nil
}

func natRuleImport(id string) (map[string]attr.Value, error) {
	return numberImport(id, "number", 1, 9999)
}

func putMatch(l configtree.Leaves, d *fwDest, where string) {
	if d == nil {
		return
	}
	putString(l, d.Address, where, "address")
	putString(l, d.Port, where, "port")
	putString(l, d.AddressGroup, where, "group", "address-group")
	putString(l, d.NetworkGroup, where, "group", "network-group")
	putString(l, d.PortGroup, where, "group", "port-group")
}

func getMatch(tree any, where string) *fwDest {
	sub, ok := lookup(tree, where)
	if !ok {
		return nil
	}
	d := &fwDest{
		Address:      getString(sub, "address"),
		Port:         getString(sub, "port"),
		AddressGroup: getString(sub, "group", "address-group"),
		NetworkGroup: getString(sub, "group", "network-group"),
		PortGroup:    getString(sub, "group", "port-group"),
	}
	if allNull(d.Address, d.Port, d.AddressGroup, d.NetworkGroup, d.PortGroup) {
		return nil
	}
	return d
}

func getNATAddress(tree any, where string) *natAddress {
	sub, ok := lookup(tree, where)
	if !ok {
		return nil
	}
	a := &natAddress{Address: getString(sub, "address"), Port: getString(sub, "port")}
	if allNull(a.Address, a.Port) {
		return nil
	}
	return a
}

func natRuleToLeaves(_ context.Context, m *natRuleModel, _ *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putString(l, m.Type, "type")
	putString(l, m.Description, "description")
	putString(l, m.InboundInterface, "inbound-interface")
	putString(l, m.OutboundInterface, "outbound-interface")
	putString(l, m.Protocol, "protocol")
	putEnable(l, m.Log, "log")
	putFlag(l, m.Disabled, "disable")
	putFlag(l, m.Exclude, "exclude")
	putMatch(l, m.Source, "source")
	putMatch(l, m.Destination, "destination")
	for where, a := range map[string]*natAddress{"inside-address": m.InsideAddress, "outside-address": m.OutsideAddress} {
		if a != nil {
			putString(l, a.Address, where, "address")
			putString(l, a.Port, where, "port")
		}
	}
	return l
}

func natRuleFromTree(_ context.Context, tree any, m *natRuleModel, _ *diag.Diagnostics) {
	m.Type = getString(tree, "type")
	m.Description = getString(tree, "description")
	m.InboundInterface = getString(tree, "inbound-interface")
	m.OutboundInterface = getString(tree, "outbound-interface")
	m.Protocol = getString(tree, "protocol")
	m.Log = getEnable(tree, "log")
	m.Disabled = getFlag(tree, "disable")
	m.Exclude = getFlag(tree, "exclude")
	m.Source = getMatch(tree, "source")
	m.Destination = getMatch(tree, "destination")
	m.InsideAddress = getNATAddress(tree, "inside-address")
	m.OutsideAddress = getNATAddress(tree, "outside-address")
}

func portsAllowed(proto types.String) bool {
	p := strings.TrimPrefix(proto.ValueString(), "!")
	return p == "tcp" || p == "udp" || p == "tcp_udp"
}

func validateNATRule(_ context.Context, m *natRuleModel, diags *diag.Diagnostics) {
	typ := m.Type.ValueString()
	if known(m.Type) && known(m.Number) {
		n := m.Number.ValueInt64()
		if typ == "destination" && n >= 5000 {
			diags.AddAttributeWarning(path.Root("number"), "Destination rule outside 1-4999",
				fmt.Sprintf("Rule %d works, but the EdgeOS web UI expects destination NAT rules in 1 to 4999 and lists this one with the source rules.", n))
		}
		if typ != "destination" && n < 5000 {
			diags.AddAttributeWarning(path.Root("number"), "Source rule outside 5000-9999",
				fmt.Sprintf("Rule %d works, but the EdgeOS web UI expects %s rules in 5000 to 9999 and lists this one with the destination rules.", n, typ))
		}
	}

	if known(m.Type) {
		exclude := known(m.Exclude) && m.Exclude.ValueBool()
		missing := func(v attr.Value) bool { return v.IsNull() }
		switch typ {
		case "destination":
			if missing(m.InboundInterface) {
				diags.AddAttributeError(path.Root("inbound_interface"), "Missing inbound_interface",
					"Destination NAT rules need inbound_interface, the interface the packets arrive on.")
			}
			if m.InsideAddress == nil && !exclude {
				diags.AddAttributeError(path.Root("inside_address"), "Missing inside_address",
					"Destination NAT rules need inside_address, the address the packets are sent to.")
			}
			if !missing(m.OutboundInterface) {
				diags.AddAttributeError(path.Root("outbound_interface"), "outbound_interface not allowed",
					"Destination NAT rules match on inbound_interface; EdgeOS refuses outbound_interface on them.")
			}
			if m.OutsideAddress != nil {
				diags.AddAttributeError(path.Root("outside_address"), "outside_address not allowed",
					"Destination NAT rules translate to inside_address; EdgeOS refuses outside_address on them.")
			}
		default:
			if missing(m.OutboundInterface) {
				diags.AddAttributeError(path.Root("outbound_interface"), "Missing outbound_interface",
					fmt.Sprintf("%s rules need outbound_interface, the interface the packets leave through.", titleCase(typ)))
			}
			if !missing(m.InboundInterface) {
				diags.AddAttributeError(path.Root("inbound_interface"), "inbound_interface not allowed",
					fmt.Sprintf("%s rules match on outbound_interface; EdgeOS refuses inbound_interface on them.", titleCase(typ)))
			}
			if m.InsideAddress != nil {
				diags.AddAttributeError(path.Root("inside_address"), "inside_address not allowed",
					fmt.Sprintf("%s rules translate the source; inside_address is only for destination rules.", titleCase(typ)))
			}
			if typ == "source" && m.OutsideAddress == nil && !exclude {
				diags.AddAttributeError(path.Root("outside_address"), "Missing outside_address",
					"Source NAT rules need outside_address. Use type = \"masquerade\" to translate to the interface address.")
			}
			if typ == "masquerade" && m.OutsideAddress != nil {
				diags.AddAttributeError(path.Root("outside_address"), "outside_address not allowed",
					"Masquerade rules always use the address of outbound_interface. Use type = \"source\" to pick an address.")
			}
		}
	}

	if !m.Protocol.IsUnknown() && !portsAllowed(m.Protocol) {
		ports := map[string]types.String{}
		if m.Source != nil {
			ports["source"] = m.Source.Port
		}
		if m.Destination != nil {
			ports["destination"] = m.Destination.Port
		}
		if m.InsideAddress != nil {
			ports["inside_address"] = m.InsideAddress.Port
		}
		if m.OutsideAddress != nil {
			ports["outside_address"] = m.OutsideAddress.Port
		}
		for _, name := range []string{"source", "destination", "inside_address", "outside_address"} {
			if p, ok := ports[name]; ok && !p.IsNull() {
				diags.AddAttributeError(path.Root(name).AtName("port"), "Port needs tcp or udp",
					"Set protocol to tcp, udp or tcp_udp to match or translate ports.")
			}
		}
	}

	for name, d := range map[string]*fwDest{"source": m.Source, "destination": m.Destination} {
		if d == nil {
			continue
		}
		if allNull(d.Address, d.Port, d.AddressGroup, d.NetworkGroup, d.PortGroup) {
			diags.AddAttributeError(path.Root(name), "Empty "+name+" block", "Set at least one attribute, or remove the block.")
		}
		if known(d.Address) && known(d.AddressGroup) {
			diags.AddAttributeError(path.Root(name), "Conflicting "+name+" match", "Set either address or address_group, not both.")
		}
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
