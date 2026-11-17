// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/netip"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// routeHop is the shape shared by a next-hop, a next-hop-interface and the
// blackhole of a static route.
type routeHop struct {
	Description types.String `tfsdk:"description"`
	Disabled    types.Bool   `tfsdk:"disabled"`
	Distance    types.Int64  `tfsdk:"distance"`
}

var routeHopType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"description": types.StringType,
	"disabled":    types.BoolType,
	"distance":    types.Int64Type,
}}

func routeHopAttributes(what string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"description": descriptionAttribute(),
		"disabled":    disabledAttribute(what),
		"distance": schema.Int64Attribute{
			MarkdownDescription: "Administrative distance from 1 to 255. Lower wins when several routes match the same " +
				"prefix. Unset uses the EdgeOS default of 1.",
			Optional:   true,
			Validators: []validator.Int64{int64validator.Between(1, 255)},
		},
	}
}

// putHop writes a hop below names. The tag node itself is always written, so
// a hop without options exists and removing the hop deletes its options too.
func putHop(l configtree.Leaves, h routeHop, names ...string) {
	sub := func(n string) []string { return append(append([]string{}, names...), n) }
	l.SetFlag(configtree.NewPath(names...))
	putString(l, h.Description, sub("description")...)
	putFlag(l, h.Disabled, sub("disable")...)
	putInt(l, h.Distance, sub("distance")...)
}

func getHop(tree any, diags *diag.Diagnostics) routeHop {
	return routeHop{
		Description: getString(tree, "description"),
		Disabled:    getFlag(tree, "disable"),
		Distance:    getInt(tree, diags, "distance"),
	}
}

// putHops writes every hop of a map attribute as a tag below names.
func putHops(ctx context.Context, l configtree.Leaves, v types.Map, diags *diag.Diagnostics, names ...string) {
	if !known(v) {
		return
	}
	hops := map[string]routeHop{}
	diags.Append(v.ElementsAs(ctx, &hops, false)...)
	for key, h := range hops {
		putHop(l, h, append(append([]string{}, names...), key)...)
	}
}

// getHops reads the tags below names into a map attribute, null when there
// are none.
func getHops(ctx context.Context, tree any, diags *diag.Diagnostics, names ...string) types.Map {
	sub, ok := lookup(tree, names...)
	keys := configtree.Children(sub)
	if !ok || len(keys) == 0 {
		return types.MapNull(routeHopType)
	}
	sort.Strings(keys)
	hops := make(map[string]routeHop, len(keys))
	for _, k := range keys {
		hs, _ := lookup(sub, k)
		hops[k] = getHop(hs, diags)
	}
	m, d := types.MapValueFrom(ctx, routeHopType, hops)
	diags.Append(d...)
	return m
}

func destinationAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Destination network in CIDR notation, such as `198.51.100.0/24` or `0.0.0.0/0` for a " +
			"default route. It must be the network address (no host bits). Changing it creates a new route.",
		Required:      true,
		Validators:    []validator.String{ipv4Prefix()},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func prefixImport(id string) (map[string]attr.Value, error) {
	pfx, err := netip.ParsePrefix(id)
	switch {
	case err != nil || !pfx.Addr().Is4():
		return nil, fmt.Errorf("%q is not an IPv4 network in CIDR notation", id)
	case pfx.Masked() != pfx:
		return nil, fmt.Errorf("%q has host bits set, the route is stored as %s", id, pfx.Masked())
	}
	return map[string]attr.Value{"destination": types.StringValue(id)}, nil
}

// protocols static route

type staticRouteModel struct {
	ID          types.String `tfsdk:"id"`
	Destination types.String `tfsdk:"destination"`
	NextHops    types.Map    `tfsdk:"next_hops"`
	Blackhole   *routeHop    `tfsdk:"blackhole"`
}

func newStaticRouteResource() resourceFactory {
	return newLeafResource(&leafSpec[staticRouteModel]{
		Name: "static_route",
		Schema: schema.Schema{
			MarkdownDescription: "A static IPv4 route through one or more gateways (`protocols static route <prefix>`), " +
				"or a blackhole route that drops the traffic.\n\n" +
				"Set `next_hops`, `blackhole` or both. With several next hops of the same distance EdgeOS spreads the " +
				"traffic over them (ECMP); with different distances the lowest one is used and the others are backups. " +
				"A blackhole with a higher distance than the next hops drops the traffic when every gateway is gone.\n\n" +
				"Routes that only name an outgoing interface live under `protocols static interface-route`, see " +
				"`edgeos_static_interface_route`.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute("The destination prefix."),
				"destination": destinationAttribute(),
				"next_hops": schema.MapNestedAttribute{
					MarkdownDescription: "Gateways for the route, keyed by the gateway's IPv4 address. Use `{}` for a gateway " +
						"without options.",
					Optional: true,
					Validators: []validator.Map{
						mapvalidator.SizeAtLeast(1),
						mapvalidator.KeysAre(ipv4Address()),
					},
					NestedObject: schema.NestedAttributeObject{Attributes: routeHopAttributes("next hop")},
				},
				"blackhole": schema.SingleNestedAttribute{
					MarkdownDescription: "Drop traffic to the destination. Use `{}` for a plain blackhole route.",
					Optional:            true,
					Attributes:          routeHopAttributes("blackhole route"),
				},
			},
		},
		Path: func(m *staticRouteModel) configtree.Path {
			return configtree.NewPath("protocols", "static", "route", m.Destination.ValueString())
		},
		ID:          func(m *staticRouteModel) string { return m.Destination.ValueString() },
		SetID:       func(m *staticRouteModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    staticRouteToLeaves,
		FromTree:    staticRouteFromTree,
		ImportAttrs: prefixImport,
		ImportHint:  "the destination prefix, for example 198.51.100.0/24",
		Validate: func(_ context.Context, m *staticRouteModel, diags *diag.Diagnostics) {
			if m.NextHops.IsNull() && m.Blackhole == nil {
				diags.AddAttributeError(path.Root("next_hops"), "No next hop",
					"Set next_hops, blackhole or both; a static route needs somewhere to send the traffic.")
			}
		},
	})
}

func staticRouteToLeaves(ctx context.Context, m *staticRouteModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putHops(ctx, l, m.NextHops, diags, "next-hop")
	if m.Blackhole != nil {
		putHop(l, *m.Blackhole, "blackhole")
	}
	return l
}

func staticRouteFromTree(ctx context.Context, tree any, m *staticRouteModel, diags *diag.Diagnostics) {
	m.NextHops = getHops(ctx, tree, diags, "next-hop")
	m.Blackhole = nil
	if bh, ok := lookup(tree, "blackhole"); ok {
		h := getHop(bh, diags)
		m.Blackhole = &h
	}
}

// protocols static interface-route

type staticInterfaceRouteModel struct {
	ID          types.String `tfsdk:"id"`
	Destination types.String `tfsdk:"destination"`
	Interfaces  types.Map    `tfsdk:"next_hop_interfaces"`
}

func newStaticInterfaceRouteResource() resourceFactory {
	return newLeafResource(&leafSpec[staticInterfaceRouteModel]{
		Name: "static_interface_route",
		Schema: schema.Schema{
			MarkdownDescription: "A static IPv4 route out of an interface without a gateway address " +
				"(`protocols static interface-route <prefix>`), for point-to-point links such as WireGuard, OpenVPN or PPPoE.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute("The destination prefix."),
				"destination": destinationAttribute(),
				"next_hop_interfaces": schema.MapNestedAttribute{
					MarkdownDescription: "Interfaces to send the traffic out of, keyed by interface name such as `wg0`. Use " +
						"`{}` for an interface without options.",
					Required: true,
					Validators: []validator.Map{
						mapvalidator.SizeAtLeast(1),
						mapvalidator.KeysAre(interfaceName()),
					},
					NestedObject: schema.NestedAttributeObject{Attributes: routeHopAttributes("interface route")},
				},
			},
		},
		Path: func(m *staticInterfaceRouteModel) configtree.Path {
			return configtree.NewPath("protocols", "static", "interface-route", m.Destination.ValueString())
		},
		ID:    func(m *staticInterfaceRouteModel) string { return m.Destination.ValueString() },
		SetID: func(m *staticInterfaceRouteModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(ctx context.Context, m *staticInterfaceRouteModel, diags *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putHops(ctx, l, m.Interfaces, diags, "next-hop-interface")
			return l
		},
		FromTree: func(ctx context.Context, tree any, m *staticInterfaceRouteModel, diags *diag.Diagnostics) {
			m.Interfaces = getHops(ctx, tree, diags, "next-hop-interface")
		},
		ImportAttrs: prefixImport,
		ImportHint:  "the destination prefix, for example 10.66.0.0/24",
	})
}
