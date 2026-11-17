// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// service dhcp-server global settings

type dhcpServerSettingsModel struct {
	ID               types.String `tfsdk:"id"`
	Disabled         types.Bool   `tfsdk:"disabled"`
	HostfileUpdate   types.Bool   `tfsdk:"hostfile_update"`
	StaticARP        types.Bool   `tfsdk:"static_arp"`
	UseDnsmasq       types.Bool   `tfsdk:"use_dnsmasq"`
	GlobalParameters types.List   `tfsdk:"global_parameters"`
}

const dhcpServerSettingsID = "dhcp-server"

func (m *dhcpServerSettingsModel) allNull() bool {
	return allNull(m.Disabled, m.HostfileUpdate, m.StaticARP, m.UseDnsmasq, m.GlobalParameters)
}

func dhcpServerSettingsToLeaves(ctx context.Context, m *dhcpServerSettingsModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putBoolString(l, m.Disabled, "disabled")
	putEnable(l, m.HostfileUpdate, "hostfile-update")
	putEnable(l, m.StaticARP, "static-arp")
	putEnable(l, m.UseDnsmasq, "use-dnsmasq")
	putStrings(ctx, l, m.GlobalParameters, diags, "global-parameters")
	return l
}

// dhcpServerSettingsFromTree refreshes only the managed attributes, or all
// of them right after import.
func dhcpServerSettingsFromTree(_ context.Context, tree any, m *dhcpServerSettingsModel, _ *diag.Diagnostics) {
	all := m.allNull()
	if all || !m.Disabled.IsNull() {
		m.Disabled = getBoolString(tree, "disabled")
	}
	if all || !m.HostfileUpdate.IsNull() {
		m.HostfileUpdate = getEnable(tree, "hostfile-update")
	}
	if all || !m.StaticARP.IsNull() {
		m.StaticARP = getEnable(tree, "static-arp")
	}
	if all || !m.UseDnsmasq.IsNull() {
		m.UseDnsmasq = getEnable(tree, "use-dnsmasq")
	}
	if all || !m.GlobalParameters.IsNull() {
		m.GlobalParameters = getList(tree, "global-parameters")
	}
}

func newDHCPServerSettingsResource() resourceFactory {
	return newLeafResource(&leafSpec[dhcpServerSettingsModel]{
		Name: "dhcp_server_settings",
		Schema: schema.Schema{
			MarkdownDescription: "The global DHCP server settings directly below `service dhcp-server`. There is only one " +
				"set per router, so the resource takes over the existing values on create and the ID is always " +
				"`dhcp-server`.\n\n" +
				"Only the attributes you set are managed. Attributes left unset are neither read nor changed, and " +
				"removing an attribute from the configuration deletes the leaf, so EdgeOS uses its default again. Import " +
				"reads every attribute. Shared networks below `shared-network-name` are never touched; manage them with " +
				"`edgeos_dhcp_shared_network` and `edgeos_dhcp_static_mapping`.\n\n" +
				"Destroying the resource deletes the leaves it manages. EdgeOS then uses its defaults: the server is " +
				"enabled, runs ISC dhcpd, and neither updates `/etc/hosts` nor adds static ARP entries.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("Always `dhcp-server`."),
				"disabled": schema.BoolAttribute{
					MarkdownDescription: "Stop the DHCP server on every shared network (`disabled true`). EdgeOS default: `false`.",
					Optional:            true,
				},
				"hostfile_update": schema.BoolAttribute{
					MarkdownDescription: "Add the host names of DHCP clients to `/etc/hosts`, so the DNS forwarder " +
						"resolves them (`hostfile-update enable`). EdgeOS default: `false`.",
					Optional: true,
				},
				"static_arp": schema.BoolAttribute{
					MarkdownDescription: "Add permanent ARP entries for the static mappings (`static-arp enable`). " +
						"EdgeOS default: `false`.",
					Optional: true,
				},
				"use_dnsmasq": schema.BoolAttribute{
					MarkdownDescription: "Serve DHCP with dnsmasq instead of ISC dhcpd (`use-dnsmasq enable`). The raw " +
						"dhcpd parameters (`global_parameters` and the `*_parameters` of shared networks) are ignored " +
						"by dnsmasq. EdgeOS default: `false`.",
					Optional: true,
				},
				"global_parameters": parameterList("Raw ISC dhcpd statements added to the top of `dhcpd.conf` " +
					"(`global-parameters`), in order, such as `option space ubnt;`. Write double quotes as `&quot;`."),
			},
		},
		Path:     func(*dhcpServerSettingsModel) configtree.Path { return configtree.NewPath("service", "dhcp-server") },
		ID:       func(*dhcpServerSettingsModel) string { return dhcpServerSettingsID },
		SetID:    func(m *dhcpServerSettingsModel, id string) { m.ID = types.StringValue(id) },
		Adopt:    true,
		ToLeaves: dhcpServerSettingsToLeaves,
		FromTree: dhcpServerSettingsFromTree,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if id != dhcpServerSettingsID {
				return nil, fmt.Errorf("there is only one set of global DHCP server settings")
			}
			return map[string]attr.Value{"id": types.StringValue(dhcpServerSettingsID)}, nil
		},
		ImportHint: "the literal ID dhcp-server",
		Validate: func(_ context.Context, m *dhcpServerSettingsModel, diags *diag.Diagnostics) {
			if m.allNull() {
				diags.AddError("Nothing to manage",
					"Set at least one of disabled, hostfile_update, static_arp, use_dnsmasq or global_parameters.")
			}
		},
	})
}
