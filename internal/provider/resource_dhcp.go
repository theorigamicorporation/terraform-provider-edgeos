// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// DHCP server shared network

type dhcpSharedNetworkModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Authoritative types.Bool   `tfsdk:"authoritative"`
	Disabled      types.Bool   `tfsdk:"disabled"`
	Parameters    types.List   `tfsdk:"shared_network_parameters"`
	Subnets       types.Map    `tfsdk:"subnets"`
}

type dhcpSubnetModel struct {
	DefaultRouter    types.String `tfsdk:"default_router"`
	DNSServers       types.List   `tfsdk:"dns_servers"`
	DomainName       types.String `tfsdk:"domain_name"`
	Lease            types.Int64  `tfsdk:"lease"`
	NTPServers       types.List   `tfsdk:"ntp_servers"`
	TimeServers      types.List   `tfsdk:"time_servers"`
	WINSServers      types.List   `tfsdk:"wins_servers"`
	Ranges           types.Set    `tfsdk:"ranges"`
	Exclude          types.Set    `tfsdk:"exclude"`
	TFTPServerName   types.String `tfsdk:"tftp_server_name"`
	BootfileName     types.String `tfsdk:"bootfile_name"`
	UnifiController  types.String `tfsdk:"unifi_controller"`
	SubnetParameters types.List   `tfsdk:"subnet_parameters"`
}

type dhcpRangeModel struct {
	Start types.String `tfsdk:"start"`
	Stop  types.String `tfsdk:"stop"`
}

var (
	dhcpRangeAttrTypes = map[string]attr.Type{"start": types.StringType, "stop": types.StringType}
	dhcpRangeType      = types.ObjectType{AttrTypes: dhcpRangeAttrTypes}
	dhcpSubnetType     = types.ObjectType{AttrTypes: map[string]attr.Type{
		"default_router":    types.StringType,
		"dns_servers":       types.ListType{ElemType: types.StringType},
		"domain_name":       types.StringType,
		"lease":             types.Int64Type,
		"ntp_servers":       types.ListType{ElemType: types.StringType},
		"time_servers":      types.ListType{ElemType: types.StringType},
		"wins_servers":      types.ListType{ElemType: types.StringType},
		"ranges":            types.SetType{ElemType: dhcpRangeType},
		"exclude":           types.SetType{ElemType: types.StringType},
		"tftp_server_name":  types.StringType,
		"bootfile_name":     types.StringType,
		"unifi_controller":  types.StringType,
		"subnet_parameters": types.ListType{ElemType: types.StringType},
	}}
)

// dhcpSubnetChildren are the nodes below a subnet that the shared network
// resource owns. static-mapping is deliberately missing.
var dhcpSubnetChildren = []string{
	"default-router", "dns-server", "domain-name", "lease", "ntp-server", "time-server", "wins-server",
	"start", "exclude", "tftp-server-name", "bootfile-name", "unifi-controller", "subnet-parameters",
}

func ipv4List(desc string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		ElementType:         types.StringType,
		Validators:          []validator.List{listvalidator.SizeAtLeast(1), listvalidator.ValueStringsAre(ipv4Address())},
	}
}

func parameterList(desc string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		ElementType:         types.StringType,
		Validators: []validator.List{
			listvalidator.SizeAtLeast(1),
			listvalidator.ValueStringsAre(edgeosValue(), stringvalidator.LengthAtLeast(1)),
		},
	}
}

func optionalString(desc string, v ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Validators: v}
}

func dhcpSubnetAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"default_router": optionalString("Gateway handed to clients (`default-router`).", ipv4Address()),
		"dns_servers":    ipv4List("DNS servers handed to clients, in order of preference (`dns-server`)."),
		"domain_name": optionalString("Domain name handed to clients, such as `home.arpa` (`domain-name`).",
			edgeosValue(), stringvalidator.LengthBetween(1, 253)),
		"lease": schema.Int64Attribute{
			MarkdownDescription: "Lease time in seconds, at least 120. EdgeOS uses 86400 (one day) when unset.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.Between(120, 4294967295)},
		},
		"ntp_servers":  ipv4List("NTP servers handed to clients (`ntp-server`, DHCP option 42)."),
		"time_servers": ipv4List("RFC 868 time servers handed to clients (`time-server`, DHCP option 4)."),
		"wins_servers": ipv4List("NetBIOS name servers handed to clients (`wins-server`)."),
		"ranges": schema.SetNestedAttribute{
			MarkdownDescription: "Address pools (`start <start> stop <stop>`). Both ends must be inside the subnet, " +
				"and pools must not overlap.",
			Optional: true,
			Validators: []validator.Set{
				setvalidator.SizeAtLeast(1),
			},
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"start": schema.StringAttribute{
					MarkdownDescription: "First address of the pool.",
					Required:            true,
					Validators:          []validator.String{ipv4Address()},
				},
				"stop": schema.StringAttribute{
					MarkdownDescription: "Last address of the pool.",
					Required:            true,
					Validators:          []validator.String{ipv4Address()},
				},
			}},
		},
		"exclude": schema.SetAttribute{
			MarkdownDescription: "Addresses inside the pools that are never leased (`exclude`).",
			Optional:            true,
			ElementType:         types.StringType,
			Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(ipv4Address())},
		},
		"tftp_server_name": optionalString("TFTP server name or address for network boot (`tftp-server-name`, DHCP option 66).",
			edgeosValue(), stringvalidator.LengthAtLeast(1)),
		"bootfile_name": optionalString("Boot file name for network boot (`bootfile-name`, DHCP option 67).",
			edgeosValue(), stringvalidator.LengthAtLeast(1)),
		"unifi_controller": optionalString("Address of a UniFi controller handed to UniFi devices (`unifi-controller`).",
			ipv4Address()),
		"subnet_parameters": parameterList("Raw ISC dhcpd statements added to the subnet block (`subnet-parameters`), " +
			"such as `option domain-search &quot;example.com&quot;;`. EdgeOS does not allow double quotes in values; " +
			"write them as `&quot;`."),
	}
}

func newDHCPSharedNetworkResource() resourceFactory {
	return newLeafResource(&leafSpec[dhcpSharedNetworkModel]{
		Name: "dhcp_shared_network",
		Schema: schema.Schema{
			MarkdownDescription: "A DHCP server shared network and its subnets (`service dhcp-server shared-network-name <name>`).\n\n" +
				"Static mappings are managed with `edgeos_dhcp_static_mapping` and are never touched when this resource " +
				"changes a subnet that stays in the config. Removing a subnet from `subnets` deletes that whole subnet " +
				"on the router, including static mappings inside it. Options of a subnet that are not modelled here " +
				"(`static-route`, `failover`, ...) are left alone unless the subnet is removed.\n\n" +
				"Destroying the resource deletes the whole shared network, including every subnet and static mapping in it, " +
				"managed by Terraform or not. Global DHCP server settings (`service dhcp-server disabled`, " +
				"`use-dnsmasq`, `hostfile-update`) are not part of this resource; use `edgeos_config` for those.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The shared network name."),
				"name": schema.StringAttribute{
					MarkdownDescription: "Shared network name, such as `LAN`. Changing it creates a new shared network.",
					Required:            true,
					Validators:          []validator.String{tagName(), stringvalidator.LengthBetween(1, 64)},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"description": descriptionAttribute(),
				"authoritative": schema.BoolAttribute{
					MarkdownDescription: "Answer as the authoritative server for the network, so clients with leases " +
						"from elsewhere get a DHCPNAK (`authoritative enable`). Unset leaves the node out, which EdgeOS treats as disabled.",
					Optional: true,
				},
				"disabled": disabledAttribute("shared network"),
				"shared_network_parameters": parameterList("Raw ISC dhcpd statements for the shared network block " +
					"(`shared-network-parameters`). Write double quotes as `&quot;`."),
				"subnets": schema.MapNestedAttribute{
					MarkdownDescription: "Subnets served by this shared network, keyed by network in CIDR notation " +
						"such as `192.168.1.0/24`. At least one is required.",
					Required: true,
					Validators: []validator.Map{
						mapvalidator.SizeAtLeast(1),
						mapvalidator.KeysAre(ipv4Prefix()),
					},
					NestedObject: schema.NestedAttributeObject{Attributes: dhcpSubnetAttributes()},
				},
			},
		},
		Path: func(m *dhcpSharedNetworkModel) configtree.Path {
			return configtree.NewPath("service", "dhcp-server", "shared-network-name", m.Name.ValueString())
		},
		ID:       func(m *dhcpSharedNetworkModel) string { return m.Name.ValueString() },
		SetID:    func(m *dhcpSharedNetworkModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: dhcpSharedNetworkToLeaves,
		FromTree: dhcpSharedNetworkFromTree,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if !tagNameRE.MatchString(id) {
				return nil, fmt.Errorf("%q is not a valid shared network name", id)
			}
			return map[string]attr.Value{"name": types.StringValue(id)}, nil
		},
		ImportHint: "the shared network name, for example LAN",
		Validate:   validateDHCPSharedNetwork,
	})
}

// knownSubnets returns the known subnet objects of a map. Unknown elements
// (possible while validating a config) are skipped.
func knownSubnets(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]dhcpSubnetModel {
	out := map[string]dhcpSubnetModel{}
	if !known(m) {
		return out
	}
	for k, v := range m.Elements() {
		obj, ok := v.(types.Object)
		if !ok || !known(obj) {
			continue
		}
		var s dhcpSubnetModel
		diags.Append(obj.As(ctx, &s, basetypes.ObjectAsOptions{})...)
		out[k] = s
	}
	return out
}

func knownRanges(ctx context.Context, s types.Set, diags *diag.Diagnostics) []dhcpRangeModel {
	if !known(s) {
		return nil
	}
	var out []dhcpRangeModel
	for _, v := range s.Elements() {
		obj, ok := v.(types.Object)
		if !ok || !known(obj) {
			continue
		}
		var r dhcpRangeModel
		diags.Append(obj.As(ctx, &r, basetypes.ObjectAsOptions{})...)
		out = append(out, r)
	}
	return out
}

func dhcpSharedNetworkToLeaves(ctx context.Context, m *dhcpSharedNetworkModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putString(l, m.Description, "description")
	putEnable(l, m.Authoritative, "authoritative")
	putFlag(l, m.Disabled, "disable")
	putStrings(ctx, l, m.Parameters, diags, "shared-network-parameters")
	for cidr, s := range knownSubnets(ctx, m.Subnets, diags) {
		// The subnet itself is owned too, so removing it from the config
		// deletes the whole subnet node.
		l.SetFlag(configtree.NewPath("subnet", cidr))
		at := func(name ...string) []string { return append([]string{"subnet", cidr}, name...) }
		putString(l, s.DefaultRouter, at("default-router")...)
		putStrings(ctx, l, s.DNSServers, diags, at("dns-server")...)
		putString(l, s.DomainName, at("domain-name")...)
		putInt(l, s.Lease, at("lease")...)
		putStrings(ctx, l, s.NTPServers, diags, at("ntp-server")...)
		putStrings(ctx, l, s.TimeServers, diags, at("time-server")...)
		putStrings(ctx, l, s.WINSServers, diags, at("wins-server")...)
		for _, r := range knownRanges(ctx, s.Ranges, diags) {
			if !known(r.Start) {
				continue
			}
			l.SetFlag(configtree.NewPath(at("start", r.Start.ValueString())...))
			putString(l, r.Stop, at("start", r.Start.ValueString(), "stop")...)
		}
		putStrings(ctx, l, s.Exclude, diags, at("exclude")...)
		putString(l, s.TFTPServerName, at("tftp-server-name")...)
		putString(l, s.BootfileName, at("bootfile-name")...)
		putString(l, s.UnifiController, at("unifi-controller")...)
		putStrings(ctx, l, s.SubnetParameters, diags, at("subnet-parameters")...)
	}
	return l
}

func dhcpSharedNetworkFromTree(ctx context.Context, tree any, m *dhcpSharedNetworkModel, diags *diag.Diagnostics) {
	m.Description = getString(tree, "description")
	m.Authoritative = getEnable(tree, "authoritative")
	m.Disabled = getFlag(tree, "disable")
	m.Parameters = getList(tree, "shared-network-parameters")

	prior := knownSubnets(ctx, m.Subnets, diags)
	subnets, _ := lookup(tree, "subnet")
	elems := map[string]attr.Value{}
	for _, cidr := range configtree.Children(subnets) {
		sub, _ := lookup(subnets, cidr)
		// A subnet that only holds static mappings belongs to
		// edgeos_dhcp_static_mapping unless this resource manages it.
		if _, managed := prior[cidr]; !managed && !hasAny(sub, dhcpSubnetChildren...) {
			continue
		}
		s := dhcpSubnetModel{
			DefaultRouter:    getString(sub, "default-router"),
			DNSServers:       getList(sub, "dns-server"),
			DomainName:       getString(sub, "domain-name"),
			Lease:            getInt(sub, diags, "lease"),
			NTPServers:       getList(sub, "ntp-server"),
			TimeServers:      getList(sub, "time-server"),
			WINSServers:      getList(sub, "wins-server"),
			Ranges:           dhcpRangesFromTree(sub),
			Exclude:          getSet(sub, "exclude"),
			TFTPServerName:   getString(sub, "tftp-server-name"),
			BootfileName:     getString(sub, "bootfile-name"),
			UnifiController:  getString(sub, "unifi-controller"),
			SubnetParameters: getList(sub, "subnet-parameters"),
		}
		obj, d := types.ObjectValueFrom(ctx, dhcpSubnetType.AttrTypes, s)
		diags.Append(d...)
		elems[cidr] = obj
	}
	if len(elems) == 0 {
		m.Subnets = types.MapNull(dhcpSubnetType)
		return
	}
	v, d := types.MapValue(dhcpSubnetType, elems)
	diags.Append(d...)
	m.Subnets = v
}

func dhcpRangesFromTree(sub any) types.Set {
	starts, ok := lookup(sub, "start")
	if !ok {
		return types.SetNull(dhcpRangeType)
	}
	var elems []attr.Value
	for _, start := range configtree.Children(starts) {
		elems = append(elems, types.ObjectValueMust(dhcpRangeAttrTypes, map[string]attr.Value{
			"start": types.StringValue(start),
			"stop":  getString(starts, start, "stop"),
		}))
	}
	if len(elems) == 0 {
		return types.SetNull(dhcpRangeType)
	}
	return types.SetValueMust(dhcpRangeType, elems)
}

func validateDHCPSharedNetwork(ctx context.Context, m *dhcpSharedNetworkModel, diags *diag.Diagnostics) {
	for cidr, s := range knownSubnets(ctx, m.Subnets, diags) {
		pfx, err := netip.ParsePrefix(cidr)
		if err != nil || !pfx.Addr().Is4() {
			continue // reported by the key validator
		}
		at := path.Root("subnets").AtMapKey(cidr)
		type span struct{ a, b netip.Addr }
		var spans []span
		starts := map[string]bool{}
		for _, r := range knownRanges(ctx, s.Ranges, diags) {
			if !known(r.Start) || !known(r.Stop) {
				continue
			}
			start, stop := r.Start.ValueString(), r.Stop.ValueString()
			if starts[start] {
				diags.AddAttributeError(at.AtName("ranges"), "Duplicate range start",
					fmt.Sprintf("Two ranges start at %s. EdgeOS keys ranges by their start address.", start))
				continue
			}
			starts[start] = true
			if err := ipv4Range(pfx, start, stop); err != nil {
				diags.AddAttributeError(at.AtName("ranges"), "Invalid DHCP range", err.Error()+".")
				continue
			}
			spans = append(spans, span{netip.MustParseAddr(start), netip.MustParseAddr(stop)})
		}
		sort.Slice(spans, func(i, j int) bool { return spans[i].a.Less(spans[j].a) })
		for i := 1; i < len(spans); i++ {
			if !spans[i-1].b.Less(spans[i].a) {
				diags.AddAttributeError(at.AtName("ranges"), "Overlapping DHCP ranges",
					fmt.Sprintf("%s-%s overlaps %s-%s.", spans[i-1].a, spans[i-1].b, spans[i].a, spans[i].b))
			}
		}
		if known(s.DefaultRouter) {
			if a, err := netip.ParseAddr(s.DefaultRouter.ValueString()); err == nil && !pfx.Contains(a) {
				diags.AddAttributeWarning(at.AtName("default_router"), "Default router outside the subnet",
					fmt.Sprintf("%s is not inside %s. Clients usually cannot reach a gateway outside their subnet.", a, cidr))
			}
		}
	}
}

// DHCP static mapping

type dhcpStaticMappingModel struct {
	ID            types.String `tfsdk:"id"`
	SharedNetwork types.String `tfsdk:"shared_network"`
	Subnet        types.String `tfsdk:"subnet"`
	Name          types.String `tfsdk:"name"`
	IPAddress     types.String `tfsdk:"ip_address"`
	MACAddress    types.String `tfsdk:"mac_address"`
	Disabled      types.Bool   `tfsdk:"disabled"`
	Parameters    types.List   `tfsdk:"static_mapping_parameters"`
}

func newDHCPStaticMappingResource() resourceFactory {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	return newLeafResource(&leafSpec[dhcpStaticMappingModel]{
		Name: "dhcp_static_mapping",
		Schema: schema.Schema{
			MarkdownDescription: "A fixed address for one client in a DHCP subnet " +
				"(`service dhcp-server shared-network-name <shared_network> subnet <subnet> static-mapping <name>`).\n\n" +
				"Reference `edgeos_dhcp_shared_network` in `shared_network` so Terraform creates the shared network " +
				"first and deletes the mapping before it. Destroying the resource deletes the mapping node.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("`<shared_network>:<subnet>:<name>`, for example `LAN:192.168.1.0/24:printer`."),
				"shared_network": schema.StringAttribute{
					MarkdownDescription: "Name of the shared network, usually `edgeos_dhcp_shared_network.x.name`. Changing it creates a new mapping.",
					Required:            true,
					Validators:          []validator.String{tagName(), stringvalidator.LengthBetween(1, 64)},
					PlanModifiers:       replace,
				},
				"subnet": schema.StringAttribute{
					MarkdownDescription: "Subnet in CIDR notation, one of the keys of the shared network's `subnets`. Changing it creates a new mapping.",
					Required:            true,
					Validators:          []validator.String{ipv4Prefix()},
					PlanModifiers:       replace,
				},
				"name": schema.StringAttribute{
					MarkdownDescription: "Name of the mapping, such as `printer`. Changing it creates a new mapping.",
					Required:            true,
					Validators:          []validator.String{tagName(), stringvalidator.LengthBetween(1, 64)},
					PlanModifiers:       replace,
				},
				"ip_address": schema.StringAttribute{
					MarkdownDescription: "Address given to the client. It must be inside `subnet`; it may be inside or outside the pools.",
					Required:            true,
					Validators:          []validator.String{ipv4Address()},
				},
				"mac_address": schema.StringAttribute{
					MarkdownDescription: "MAC address of the client in lower case, such as `02:00:5e:00:53:20`.",
					Required:            true,
					Validators:          []validator.String{macAddress()},
				},
				"disabled": disabledAttribute("mapping"),
				"static_mapping_parameters": parameterList("Raw ISC dhcpd statements added to the host block " +
					"(`static-mapping-parameters`). Write double quotes as `&quot;`."),
			},
		},
		Path: func(m *dhcpStaticMappingModel) configtree.Path {
			return configtree.NewPath("service", "dhcp-server", "shared-network-name", m.SharedNetwork.ValueString(),
				"subnet", m.Subnet.ValueString(), "static-mapping", m.Name.ValueString())
		},
		ID: func(m *dhcpStaticMappingModel) string {
			return strings.Join([]string{m.SharedNetwork.ValueString(), m.Subnet.ValueString(), m.Name.ValueString()}, ":")
		},
		SetID: func(m *dhcpStaticMappingModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(ctx context.Context, m *dhcpStaticMappingModel, diags *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putString(l, m.IPAddress, "ip-address")
			putString(l, m.MACAddress, "mac-address")
			putFlag(l, m.Disabled, "disable")
			putStrings(ctx, l, m.Parameters, diags, "static-mapping-parameters")
			return l
		},
		FromTree: func(_ context.Context, tree any, m *dhcpStaticMappingModel, _ *diag.Diagnostics) {
			m.IPAddress = getString(tree, "ip-address")
			m.MACAddress = getString(tree, "mac-address")
			if known(m.MACAddress) {
				// The router keeps what it was given; compare in lower case.
				m.MACAddress = types.StringValue(strings.ToLower(m.MACAddress.ValueString()))
			}
			m.Disabled = getFlag(tree, "disable")
			m.Parameters = getList(tree, "static-mapping-parameters")
		},
		ImportAttrs: dhcpStaticMappingImport,
		ImportHint:  "<shared_network>:<subnet>:<name>, for example LAN:192.168.1.0/24:printer",
		Validate: func(_ context.Context, m *dhcpStaticMappingModel, diags *diag.Diagnostics) {
			if !known(m.Subnet) || !known(m.IPAddress) {
				return
			}
			pfx, err1 := netip.ParsePrefix(m.Subnet.ValueString())
			ip, err2 := netip.ParseAddr(m.IPAddress.ValueString())
			if err1 != nil || err2 != nil {
				return // reported by the attribute validators
			}
			if !pfx.Contains(ip) {
				diags.AddAttributeError(path.Root("ip_address"), "Address outside the subnet",
					fmt.Sprintf("%s is not inside %s.", ip, pfx))
			}
		},
	})
}

func dhcpStaticMappingImport(id string) (map[string]attr.Value, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("want three parts separated by ':'")
	}
	network, subnet, name := parts[0], parts[1], parts[2]
	if !tagNameRE.MatchString(network) {
		return nil, fmt.Errorf("%q is not a valid shared network name", network)
	}
	if pfx, err := netip.ParsePrefix(subnet); err != nil || !pfx.Addr().Is4() || pfx.Masked() != pfx {
		return nil, fmt.Errorf("%q is not an IPv4 network such as 192.168.1.0/24", subnet)
	}
	if !tagNameRE.MatchString(name) {
		return nil, fmt.Errorf("%q is not a valid mapping name", name)
	}
	return map[string]attr.Value{
		"shared_network": types.StringValue(network),
		"subnet":         types.StringValue(subnet),
		"name":           types.StringValue(name),
	}, nil
}
