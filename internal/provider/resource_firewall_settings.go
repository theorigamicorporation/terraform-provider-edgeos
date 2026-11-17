// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// firewall global options

type firewallSettingsModel struct {
	ID                   types.String `tfsdk:"id"`
	AllPing              types.Bool   `tfsdk:"all_ping"`
	BroadcastPing        types.Bool   `tfsdk:"broadcast_ping"`
	IPSrcRoute           types.Bool   `tfsdk:"ip_src_route"`
	IPv6SrcRoute         types.Bool   `tfsdk:"ipv6_src_route"`
	IPv6ReceiveRedirects types.Bool   `tfsdk:"ipv6_receive_redirects"`
	LogMartians          types.Bool   `tfsdk:"log_martians"`
	ReceiveRedirects     types.Bool   `tfsdk:"receive_redirects"`
	SendRedirects        types.Bool   `tfsdk:"send_redirects"`
	SynCookies           types.Bool   `tfsdk:"syn_cookies"`
	SourceValidation     types.String `tfsdk:"source_validation"`
}

const firewallSettingsID = "firewall"

// firewallToggle is one enable/disable leaf directly below firewall.
type firewallToggle struct {
	attr string
	node string
	on   bool // the EdgeOS default
	desc string
	get  func(m *firewallSettingsModel) *types.Bool
}

var firewallToggles = []firewallToggle{
	{"all_ping", "all-ping", true, "Answer ICMP echo requests sent to the router",
		func(m *firewallSettingsModel) *types.Bool { return &m.AllPing }},
	{"broadcast_ping", "broadcast-ping", false, "Answer ICMP echo requests sent to a broadcast address",
		func(m *firewallSettingsModel) *types.Bool { return &m.BroadcastPing }},
	{"ip_src_route", "ip-src-route", false, "Accept IPv4 packets with the source route option",
		func(m *firewallSettingsModel) *types.Bool { return &m.IPSrcRoute }},
	{"ipv6_src_route", "ipv6-src-route", false, "Accept IPv6 packets with a routing header",
		func(m *firewallSettingsModel) *types.Bool { return &m.IPv6SrcRoute }},
	{"ipv6_receive_redirects", "ipv6-receive-redirects", false, "Accept ICMPv6 redirects",
		func(m *firewallSettingsModel) *types.Bool { return &m.IPv6ReceiveRedirects }},
	{"log_martians", "log-martians", true, "Log IPv4 packets with impossible source addresses",
		func(m *firewallSettingsModel) *types.Bool { return &m.LogMartians }},
	{"receive_redirects", "receive-redirects", false, "Accept ICMP redirects",
		func(m *firewallSettingsModel) *types.Bool { return &m.ReceiveRedirects }},
	{"send_redirects", "send-redirects", true, "Send ICMP redirects",
		func(m *firewallSettingsModel) *types.Bool { return &m.SendRedirects }},
	{"syn_cookies", "syn-cookies", true, "Use TCP SYN cookies against SYN floods",
		func(m *firewallSettingsModel) *types.Bool { return &m.SynCookies }},
}

// firewallSourceValidationDefault is the EdgeOS default for source-validation.
const firewallSourceValidationDefault = "disable"

func (m *firewallSettingsModel) allNull() bool {
	vals := []attr.Value{m.SourceValidation}
	for _, t := range firewallToggles {
		vals = append(vals, *t.get(m))
	}
	return allNull(vals...)
}

func enableWord(on bool) string {
	if on {
		return "enable"
	}
	return "disable"
}

func firewallSettingsSchema() schema.Schema {
	attrs := map[string]schema.Attribute{
		"id": idAttribute("Always `firewall`."),
		"source_validation": schema.StringAttribute{
			MarkdownDescription: "Reverse path filtering for IPv4 (`source-validation`): `disable`, `strict` (drop packets " +
				"that would not be routed back out of the interface they came in on) or `loose` (drop packets whose " +
				"source is not reachable at all). EdgeOS default: `disable`.",
			Optional:   true,
			Validators: []validator.String{stringvalidator.OneOf("disable", "strict", "loose")},
		},
	}
	for _, t := range firewallToggles {
		attrs[t.attr] = schema.BoolAttribute{
			MarkdownDescription: fmt.Sprintf("%s (`%s enable`). EdgeOS default: `%t`.", t.desc, t.node, t.on),
			Optional:            true,
		}
	}
	return schema.Schema{
		MarkdownDescription: "The global firewall options directly below `firewall`, such as `all-ping`, `syn-cookies` " +
			"and `source-validation`. These are kernel network settings that apply to the whole router. There is only " +
			"one set per router, so the resource takes over the existing values on create and the ID is always " +
			"`firewall`.\n\n" +
			"Only the attributes you set are managed. Attributes left unset are neither read nor changed, and removing " +
			"an attribute from the configuration deletes the leaf, so EdgeOS uses its default again. Import reads every " +
			"attribute. Rule sets (`firewall name`, `firewall ipv6-name`, `firewall modify`), groups and " +
			"`firewall options` are never touched; manage rule sets with `edgeos_firewall_name` and " +
			"`edgeos_firewall_rule`.\n\n" +
			"Destroying the resource writes the EdgeOS defaults back for every managed option: `all-ping`, " +
			"`log-martians`, `send-redirects` and `syn-cookies` enabled, the rest disabled.",
		Attributes: attrs,
	}
}

func firewallSettingsToLeaves(_ context.Context, m *firewallSettingsModel, _ *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	for _, t := range firewallToggles {
		putEnable(l, *t.get(m), t.node)
	}
	putString(l, m.SourceValidation, "source-validation")
	return l
}

// firewallSettingsFromTree refreshes only the managed attributes, or all of
// them right after import (the config validation needs at least one set).
func firewallSettingsFromTree(_ context.Context, tree any, m *firewallSettingsModel, _ *diag.Diagnostics) {
	all := m.allNull()
	for _, t := range firewallToggles {
		if v := t.get(m); all || !v.IsNull() {
			*v = getEnable(tree, t.node)
		}
	}
	if all || !m.SourceValidation.IsNull() {
		m.SourceValidation = getString(tree, "source-validation")
	}
}

// firewallSettingsDefaults are the values Delete writes back.
func firewallSettingsDefaults(*firewallSettingsModel) configtree.Leaves {
	l := configtree.NewLeaves()
	for _, t := range firewallToggles {
		l.SetSingle(configtree.NewPath(t.node), enableWord(t.on))
	}
	l.SetSingle(configtree.NewPath("source-validation"), firewallSourceValidationDefault)
	return l
}

func newFirewallSettingsResource() resourceFactory {
	return newLeafResource(&leafSpec[firewallSettingsModel]{
		Name:     "firewall_settings",
		Schema:   firewallSettingsSchema(),
		Path:     func(*firewallSettingsModel) configtree.Path { return configtree.NewPath("firewall") },
		ID:       func(*firewallSettingsModel) string { return firewallSettingsID },
		SetID:    func(m *firewallSettingsModel, id string) { m.ID = types.StringValue(id) },
		Adopt:    true,
		ToLeaves: firewallSettingsToLeaves,
		FromTree: firewallSettingsFromTree,
		Reset:    firewallSettingsDefaults,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if id != firewallSettingsID {
				return nil, fmt.Errorf("there is only one set of global firewall options")
			}
			return map[string]attr.Value{"id": types.StringValue(firewallSettingsID)}, nil
		},
		ImportHint: "the literal ID firewall",
		Validate: func(_ context.Context, m *firewallSettingsModel, diags *diag.Diagnostics) {
			if m.allNull() {
				diags.AddError("Nothing to manage", "Set at least one of the firewall options.")
			}
		},
	})
}
