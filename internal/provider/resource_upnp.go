// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// service upnp2

var upnpRuleRE = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)

type upnpModel struct {
	ID         types.String `tfsdk:"id"`
	ListenOn   types.Set    `tfsdk:"listen_on"`
	WAN        types.String `tfsdk:"wan"`
	NATPMP     types.Bool   `tfsdk:"nat_pmp"`
	SecureMode types.Bool   `tfsdk:"secure_mode"`
	ACLRules   types.Map    `tfsdk:"acl_rules"`
}

type upnpACLRuleModel struct {
	Action       types.String `tfsdk:"action"`
	Description  types.String `tfsdk:"description"`
	Subnet       types.String `tfsdk:"subnet"`
	ExternalPort types.String `tfsdk:"external_port"`
	LocalPort    types.String `tfsdk:"local_port"`
}

const upnpID = "upnp2"

var upnpACLRuleType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"action":        types.StringType,
	"description":   types.StringType,
	"subnet":        types.StringType,
	"external_port": types.StringType,
	"local_port":    types.StringType,
}}

func upnpPortRange(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Validators:          []validator.String{stringvalidator.RegexMatches(portRangeRE, "must be a port or a range such as 1024-65535")},
	}
}

func upnpSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "The UPnP IGD and NAT-PMP service (`service upnp2`), which lets LAN hosts open port " +
			"forwards on the WAN interface by themselves. There is one per router and the ID is always `upnp2`.\n\n" +
			"The resource owns `service upnp2`: creating it fails if UPnP is already configured (import it instead), " +
			"every modelled attribute is read back, and destroying it deletes the whole node, which turns UPnP off as " +
			"on a factory reset router. Options that are not modelled here (`port`, `bit-rate`) are left alone until " +
			"the resource is destroyed.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Always `upnp2`."),
			"listen_on": schema.SetAttribute{
				MarkdownDescription: "LAN interfaces UPnP and NAT-PMP requests are accepted on, such as `[\"switch0\"]`.",
				Required:            true,
				ElementType:         types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(interfaceName()),
				},
			},
			"wan": schema.StringAttribute{
				MarkdownDescription: "Interface the port forwards are opened on, such as `eth0` or `pppoe0`.",
				Required:            true,
				Validators:          []validator.String{interfaceName()},
			},
			"nat_pmp": schema.BoolAttribute{
				MarkdownDescription: "Answer NAT-PMP requests as well as UPnP (`nat-pmp enable`). Unset leaves the node out.",
				Optional:            true,
			},
			"secure_mode": schema.BoolAttribute{
				MarkdownDescription: "Only let a host open forwards to its own address (`secure-mode enable`). Unset leaves the node out.",
				Optional:            true,
			},
			"acl_rules": schema.MapNestedAttribute{
				MarkdownDescription: "Access control rules (`acl rule <number>`), keyed by rule number from `1` to `9999`. " +
					"miniupnpd checks them in order and uses the first match. Without rules every request from " +
					"`listen_on` is allowed. Rules not in the map are removed.",
				Optional: true,
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
					mapvalidator.KeysAre(stringvalidator.RegexMatches(upnpRuleRE, "must be a rule number from 1 to 9999")),
				},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"action": schema.StringAttribute{
						MarkdownDescription: "`allow` or `deny`.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.OneOf("allow", "deny")},
					},
					"description": descriptionAttribute(),
					"subnet": schema.StringAttribute{
						MarkdownDescription: "LAN hosts the rule applies to, in CIDR notation such as `192.168.1.0/24`.",
						Optional:            true,
						Validators:          []validator.String{ipv4Prefix()},
					},
					"external_port": upnpPortRange("WAN port or range the rule applies to, such as `1024-65535`."),
					"local_port":    upnpPortRange("LAN port or range the rule applies to, such as `1024-65535`."),
				}},
			},
		},
	}
}

// knownACLRules returns the known rule objects of a map.
func knownACLRules(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]upnpACLRuleModel {
	out := map[string]upnpACLRuleModel{}
	if !known(m) {
		return out
	}
	for k, v := range m.Elements() {
		obj, ok := v.(types.Object)
		if !ok || !known(obj) {
			continue
		}
		var r upnpACLRuleModel
		diags.Append(obj.As(ctx, &r, basetypes.ObjectAsOptions{})...)
		out[k] = r
	}
	return out
}

func upnpToLeaves(ctx context.Context, m *upnpModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putStrings(ctx, l, m.ListenOn, diags, "listen-on")
	putString(l, m.WAN, "wan")
	putEnable(l, m.NATPMP, "nat-pmp")
	putEnable(l, m.SecureMode, "secure-mode")
	for n, r := range knownACLRules(ctx, m.ACLRules, diags) {
		// The rule itself is owned, so removing it deletes the whole node.
		l.SetFlag(configtree.NewPath("acl", "rule", n))
		at := func(name string) []string { return []string{"acl", "rule", n, name} }
		putString(l, r.Action, at("action")...)
		putString(l, r.Description, at("description")...)
		putString(l, r.Subnet, at("subnet")...)
		putString(l, r.ExternalPort, at("external-port")...)
		putString(l, r.LocalPort, at("local-port")...)
	}
	return l
}

func upnpFromTree(ctx context.Context, tree any, m *upnpModel, diags *diag.Diagnostics) {
	m.ListenOn = getSet(tree, "listen-on")
	m.WAN = getString(tree, "wan")
	m.NATPMP = getEnable(tree, "nat-pmp")
	m.SecureMode = getEnable(tree, "secure-mode")

	rules, _ := lookup(tree, "acl", "rule")
	elems := map[string]attr.Value{}
	for _, n := range configtree.Children(rules) {
		rule, _ := lookup(rules, n)
		r := upnpACLRuleModel{
			Action:       getString(rule, "action"),
			Description:  getString(rule, "description"),
			Subnet:       getString(rule, "subnet"),
			ExternalPort: getString(rule, "external-port"),
			LocalPort:    getString(rule, "local-port"),
		}
		obj, d := types.ObjectValueFrom(ctx, upnpACLRuleType.AttrTypes, r)
		diags.Append(d...)
		elems[n] = obj
	}
	if len(elems) == 0 {
		m.ACLRules = types.MapNull(upnpACLRuleType)
		return
	}
	v, d := types.MapValue(upnpACLRuleType, elems)
	diags.Append(d...)
	m.ACLRules = v
}

func validateUPnP(ctx context.Context, m *upnpModel, diags *diag.Diagnostics) {
	if !known(m.WAN) || !known(m.ListenOn) {
		return
	}
	var lan []string
	diags.Append(m.ListenOn.ElementsAs(ctx, &lan, true)...)
	sort.Strings(lan)
	for _, s := range lan {
		if s == m.WAN.ValueString() {
			diags.AddAttributeError(path.Root("listen_on"), "WAN interface listed as LAN",
				fmt.Sprintf("%s is the wan interface and cannot also be in listen_on.", s))
		}
	}
}

func newUPnPResource() resourceFactory {
	return newLeafResource(&leafSpec[upnpModel]{
		Name:     "service_upnp",
		Schema:   upnpSchema(),
		Path:     func(*upnpModel) configtree.Path { return configtree.NewPath("service", "upnp2") },
		ID:       func(*upnpModel) string { return upnpID },
		SetID:    func(m *upnpModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: upnpToLeaves,
		FromTree: upnpFromTree,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if id != upnpID {
				return nil, fmt.Errorf("there is only one UPnP service")
			}
			return map[string]attr.Value{"id": types.StringValue(upnpID)}, nil
		},
		ImportHint: "the literal ID upnp2",
		Validate:   validateUPnP,
	})
}
