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

type systemOffloadModel struct {
	ID             types.String `tfsdk:"id"`
	HWNAT          types.Bool   `tfsdk:"hwnat"`
	IPsec          types.Bool   `tfsdk:"ipsec"`
	IPv4Forwarding types.Bool   `tfsdk:"ipv4_forwarding"`
	IPv4VLAN       types.Bool   `tfsdk:"ipv4_vlan"`
	IPv4PPPoE      types.Bool   `tfsdk:"ipv4_pppoe"`
	IPv4GRE        types.Bool   `tfsdk:"ipv4_gre"`
	IPv4Bonding    types.Bool   `tfsdk:"ipv4_bonding"`
	IPv6Forwarding types.Bool   `tfsdk:"ipv6_forwarding"`
	IPv6VLAN       types.Bool   `tfsdk:"ipv6_vlan"`
	IPv6PPPoE      types.Bool   `tfsdk:"ipv6_pppoe"`
	IPv6Bonding    types.Bool   `tfsdk:"ipv6_bonding"`
}

const systemOffloadID = "offload"

// offloadKnob maps one attribute to its node below system offload.
type offloadKnob struct {
	attr string
	path []string
	get  func(m *systemOffloadModel) *types.Bool
	desc string
}

const (
	hwCavium   = "Cavium models (EdgeRouter Lite, PoE, 4, 6P, 12, Pro, 8-XG, Infinity)"
	hwMediaTek = "MediaTek models (EdgeRouter X, X SFP, 10X and EdgePoint R6)"
)

var offloadKnobs = []offloadKnob{
	{"hwnat", []string{"hwnat"}, func(m *systemOffloadModel) *types.Bool { return &m.HWNAT },
		"Hardware NAT and forwarding offload (`hwnat`). " + hwMediaTek + " only."},
	{"ipsec", []string{"ipsec"}, func(m *systemOffloadModel) *types.Bool { return &m.IPsec },
		"IPsec ESP offload (`ipsec`). Cavium and MediaTek models."},
	{"ipv4_forwarding", []string{"ipv4", "forwarding"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv4Forwarding },
		"IPv4 forwarding offload (`ipv4 forwarding`). " + hwCavium + " only."},
	{"ipv4_vlan", []string{"ipv4", "vlan"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv4VLAN },
		"IPv4 offload for VLAN interfaces (`ipv4 vlan`). Cavium models only."},
	{"ipv4_pppoe", []string{"ipv4", "pppoe"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv4PPPoE },
		"IPv4 offload for PPPoE interfaces (`ipv4 pppoe`). Cavium models only."},
	{"ipv4_gre", []string{"ipv4", "gre"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv4GRE },
		"IPv4 offload for GRE tunnels (`ipv4 gre`). Cavium models only."},
	{"ipv4_bonding", []string{"ipv4", "bonding"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv4Bonding },
		"IPv4 offload for bonded interfaces (`ipv4 bonding`). Cavium models only."},
	{"ipv6_forwarding", []string{"ipv6", "forwarding"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv6Forwarding },
		"IPv6 forwarding offload (`ipv6 forwarding`). Cavium models only."},
	{"ipv6_vlan", []string{"ipv6", "vlan"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv6VLAN },
		"IPv6 offload for VLAN interfaces (`ipv6 vlan`). Cavium models only."},
	{"ipv6_pppoe", []string{"ipv6", "pppoe"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv6PPPoE },
		"IPv6 offload for PPPoE interfaces (`ipv6 pppoe`). Cavium models only."},
	{"ipv6_bonding", []string{"ipv6", "bonding"}, func(m *systemOffloadModel) *types.Bool { return &m.IPv6Bonding },
		"IPv6 offload for bonded interfaces (`ipv6 bonding`). Cavium models only."},
}

func systemOffloadSchema() schema.Schema {
	attrs := map[string]schema.Attribute{"id": idAttribute("Always `offload`.")}
	for _, k := range offloadKnobs {
		attrs[k.attr] = schema.BoolAttribute{
			MarkdownDescription: k.desc + " `true` writes `enable`, `false` writes `disable`, unset leaves it alone.",
			Optional:            true,
		}
	}
	return schema.Schema{
		MarkdownDescription: "Hardware offload settings (`system offload`). There is one per router; the resource takes " +
			"over the existing node.\n\n" +
			"Which knobs do anything depends on the hardware:\n\n" +
			"- " + hwMediaTek + ": `hwnat` and `ipsec`.\n" +
			"- " + hwCavium + ": the `ipv4_*` and `ipv6_*` knobs and `ipsec`.\n\n" +
			"EdgeOS accepts every knob on every model, so the provider cannot tell you that a knob does nothing on " +
			"yours. Some offload changes only take effect after a reboot, and the provider never reboots the router.\n\n" +
			"Only the attributes you set are managed; the rest of `system offload` is neither read nor changed. " +
			"Removing an attribute from the configuration removes that node from the router, and destroying the " +
			"resource removes every node it manages, so EdgeOS falls back to its default for the model. Import " +
			"reads every attribute.",
		Attributes: attrs,
	}
}

func systemOffloadToLeaves(_ context.Context, m *systemOffloadModel, _ *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	for _, k := range offloadKnobs {
		putEnable(l, *k.get(m), k.path...)
	}
	return l
}

func systemOffloadFromTree(_ context.Context, tree any, m *systemOffloadModel, _ *diag.Diagnostics) {
	// The ID is null only right after an import, which reads everything.
	all := m.ID.IsNull()
	for _, k := range offloadKnobs {
		if v := k.get(m); all || !v.IsNull() {
			*v = getEnable(tree, k.path...)
		}
	}
}

func systemOffloadValidate(_ context.Context, m *systemOffloadModel, diags *diag.Diagnostics) {
	for _, k := range offloadKnobs {
		if !k.get(m).IsNull() {
			return
		}
	}
	diags.AddError("Nothing to manage", "Set at least one offload attribute, such as hwnat = true.")
}

// importReadingAll accepts only the fixed ID and nulls the ID, which marks the
// state as imported so the read that follows picks up every attribute.
func importReadingAll(want string) func(string) (map[string]attr.Value, error) {
	return func(id string) (map[string]attr.Value, error) {
		if id != want {
			return nil, fmt.Errorf("the only valid ID is %q", want)
		}
		return map[string]attr.Value{"id": types.StringNull()}, nil
	}
}

func newSystemOffloadResource() resourceFactory {
	return newLeafResource(&leafSpec[systemOffloadModel]{
		Name:        "system_offload",
		Schema:      systemOffloadSchema(),
		Path:        func(*systemOffloadModel) configtree.Path { return configtree.NewPath("system", "offload") },
		ID:          func(*systemOffloadModel) string { return systemOffloadID },
		SetID:       func(m *systemOffloadModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    systemOffloadToLeaves,
		FromTree:    systemOffloadFromTree,
		ImportAttrs: importReadingAll(systemOffloadID),
		ImportHint:  `"offload"`,
		Adopt:       true,
		Validate:    systemOffloadValidate,
	})
}
