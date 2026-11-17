// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Settings shared by physical ethernet ports and their VLAN subinterfaces.

type ifaceSettings struct {
	Description   types.String      `tfsdk:"description"`
	Addresses     types.Set         `tfsdk:"addresses"`
	MTU           types.Int64       `tfsdk:"mtu"`
	MAC           types.String      `tfsdk:"mac"`
	Disabled      types.Bool        `tfsdk:"disabled"`
	FirewallIn    types.String      `tfsdk:"firewall_in"`
	FirewallOut   types.String      `tfsdk:"firewall_out"`
	FirewallLocal types.String      `tfsdk:"firewall_local"`
	DHCPOptions   *dhcpOptionsModel `tfsdk:"dhcp_options"`
}

type dhcpOptionsModel struct {
	DefaultRoute         types.Bool  `tfsdk:"default_route"`
	DefaultRouteDistance types.Int64 `tfsdk:"default_route_distance"`
	NameServer           types.Bool  `tfsdk:"name_server"`
}

// addressValidator accepts an address with prefix length or dhcp/dhcpv6.
type addressValidator struct{}

func interfaceAddress() validator.String { return addressValidator{} }

func (addressValidator) Description(context.Context) string {
	return "must be an address with prefix length such as 192.0.2.1/24, or dhcp or dhcpv6"
}

func (v addressValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v addressValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()
	if s == "dhcp" || s == "dhcpv6" {
		return
	}
	if _, err := netip.ParsePrefix(s); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid interface address", fmt.Sprintf("%q %s.", s, v.Description(ctx)))
	}
}

func ifaceAttributes(what string, disabled schema.BoolAttribute) map[string]schema.Attribute {
	fw := func(dir, desc string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: fmt.Sprintf("Name of the IPv4 firewall rule set applied to %s (`firewall %s name`). "+
				"The rule set has to exist, usually `edgeos_firewall_name.x.name`; EdgeOS refuses the commit otherwise.", desc, dir),
			Optional:   true,
			Validators: []validator.String{firewallName()},
		}
	}
	return map[string]schema.Attribute{
		"description": descriptionAttribute(),
		"addresses": schema.SetAttribute{
			MarkdownDescription: fmt.Sprintf("Addresses of the %s: addresses with prefix length such as `192.0.2.1/24` or "+
				"`2001:db8::1/64`, or `dhcp` / `dhcpv6` to use a DHCP client.", what),
			Optional:    true,
			ElementType: types.StringType,
			Validators: []validator.Set{
				setvalidator.SizeAtLeast(1),
				setvalidator.ValueStringsAre(interfaceAddress()),
			},
		},
		"mtu": schema.Int64Attribute{
			MarkdownDescription: "MTU from 68 to 9000. The hardware may support less.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.Between(68, 9000)},
		},
		"mac": schema.StringAttribute{
			MarkdownDescription: "MAC address to use instead of the hardware address, in lower case.",
			Optional:            true,
			Validators:          []validator.String{macAddress()},
		},
		"disabled":       disabled,
		"firewall_in":    fw("in", "traffic coming in on the interface and forwarded"),
		"firewall_out":   fw("out", "traffic forwarded out of the interface"),
		"firewall_local": fw("local", "traffic coming in on the interface for the router itself"),
		"dhcp_options": schema.SingleNestedAttribute{
			MarkdownDescription: "DHCP client options, used with `addresses = [\"dhcp\"]`. Client and global options " +
				"(`client-option`, `global-option`) are not managed here and are left alone.",
			Optional: true,
			Attributes: map[string]schema.Attribute{
				"default_route": schema.BoolAttribute{
					MarkdownDescription: "Install the default route the DHCP server hands out. `true` writes `update`, `false` writes `no-update`.",
					Optional:            true,
				},
				"default_route_distance": schema.Int64Attribute{
					MarkdownDescription: "Administrative distance of the default route learned from DHCP, from 1 to 255.",
					Optional:            true,
					Validators:          []validator.Int64{int64validator.Between(1, 255)},
				},
				"name_server": schema.BoolAttribute{
					MarkdownDescription: "Use the DNS servers the DHCP server hands out. `true` writes `update`, `false` writes `no-update`.",
					Optional:            true,
				},
			},
		},
	}
}

func putUpdate(l configtree.Leaves, v types.Bool, names ...string) {
	if known(v) {
		s := "no-update"
		if v.ValueBool() {
			s = "update"
		}
		l.SetSingle(configtree.NewPath(names...), s)
	}
}

func getUpdate(tree any, names ...string) types.Bool {
	s := getString(tree, names...)
	if s.IsNull() {
		return types.BoolNull()
	}
	return types.BoolValue(s.ValueString() == "update")
}

func ifaceToLeaves(ctx context.Context, l configtree.Leaves, s *ifaceSettings, diags *diag.Diagnostics) {
	putString(l, s.Description, "description")
	putStrings(ctx, l, s.Addresses, diags, "address")
	putInt(l, s.MTU, "mtu")
	putString(l, s.MAC, "mac")
	putFlag(l, s.Disabled, "disable")
	putString(l, s.FirewallIn, "firewall", "in", "name")
	putString(l, s.FirewallOut, "firewall", "out", "name")
	putString(l, s.FirewallLocal, "firewall", "local", "name")
	if d := s.DHCPOptions; d != nil {
		putUpdate(l, d.DefaultRoute, "dhcp-options", "default-route")
		putInt(l, d.DefaultRouteDistance, "dhcp-options", "default-route-distance")
		putUpdate(l, d.NameServer, "dhcp-options", "name-server")
	}
}

// ifaceFromTree reads the settings from the interface subtree. With all set
// every attribute is read (tag instances and import); otherwise only the
// attributes that are not null in s, so settings the configuration leaves
// out stay unmanaged on an adopted port.
func ifaceFromTree(tree any, s *ifaceSettings, all bool, diags *diag.Diagnostics) {
	want := func(v attr.Value) bool { return all || !v.IsNull() }
	if want(s.Description) {
		s.Description = getString(tree, "description")
	}
	if want(s.Addresses) {
		s.Addresses = getSet(tree, "address")
	}
	if want(s.MTU) {
		s.MTU = getInt(tree, diags, "mtu")
	}
	if want(s.MAC) {
		s.MAC = getString(tree, "mac")
	}
	if want(s.Disabled) {
		s.Disabled = getFlag(tree, "disable")
	}
	if want(s.FirewallIn) {
		s.FirewallIn = getString(tree, "firewall", "in", "name")
	}
	if want(s.FirewallOut) {
		s.FirewallOut = getString(tree, "firewall", "out", "name")
	}
	if want(s.FirewallLocal) {
		s.FirewallLocal = getString(tree, "firewall", "local", "name")
	}
	prior := s.DHCPOptions
	if prior == nil {
		if !all {
			return
		}
		prior = &dhcpOptionsModel{}
	}
	d := &dhcpOptionsModel{DefaultRoute: prior.DefaultRoute, DefaultRouteDistance: prior.DefaultRouteDistance, NameServer: prior.NameServer}
	if want(d.DefaultRoute) {
		d.DefaultRoute = getUpdate(tree, "dhcp-options", "default-route")
	}
	if want(d.DefaultRouteDistance) {
		d.DefaultRouteDistance = getInt(tree, diags, "dhcp-options", "default-route-distance")
	}
	if want(d.NameServer) {
		d.NameServer = getUpdate(tree, "dhcp-options", "name-server")
	}
	s.DHCPOptions = nil
	if !allNull(d.DefaultRoute, d.DefaultRouteDistance, d.NameServer) {
		s.DHCPOptions = d
	}
}

func validateIface(s *ifaceSettings, diags *diag.Diagnostics) {
	if d := s.DHCPOptions; d != nil && allNull(d.DefaultRoute, d.DefaultRouteDistance, d.NameServer) {
		diags.AddAttributeError(path.Root("dhcp_options"), "Empty dhcp_options block", "Set at least one attribute, or remove the block.")
	}
}

// edgeos_interface_ethernet

type ethernetModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Duplex types.String `tfsdk:"duplex"`
	Speed  types.String `tfsdk:"speed"`
	ifaceSettings
}

func ethernetSchema() schema.Schema {
	attrs := ifaceAttributes("port", schema.BoolAttribute{
		MarkdownDescription: "`true` switches the port off (the `disable` node), `false` makes sure it is on. " +
			"Leave it unset to not manage it.",
		Optional: true,
	})
	attrs["id"] = idAttribute("The interface name.")
	attrs["name"] = schema.StringAttribute{
		MarkdownDescription: "Interface name, such as `eth0`. The port has to exist on the router. Changing it manages a different port.",
		Required:            true,
		Validators:          []validator.String{ethernetName()},
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	speedDuplexNote := " Set `duplex` and `speed` together: both `auto`, or both fixed. When unset, Terraform reads " +
		"the value from the port and leaves it alone."
	attrs["duplex"] = schema.StringAttribute{
		MarkdownDescription: "`auto`, `half` or `full`." + speedDuplexNote,
		Optional:            true,
		Computed:            true,
		Validators:          []validator.String{stringvalidator.OneOf("auto", "half", "full")},
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attrs["speed"] = schema.StringAttribute{
		MarkdownDescription: "`auto`, `10`, `100`, `1000` or `10000` (Mbit/s)." + speedDuplexNote,
		Optional:            true,
		Computed:            true,
		Validators:          []validator.String{stringvalidator.OneOf("auto", "10", "100", "1000", "10000")},
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	return schema.Schema{
		MarkdownDescription: "Settings of a physical ethernet port (`interfaces ethernet <name>`). The port must already " +
			"exist; creating the resource takes it over and never creates or deletes the interface itself.\n\n" +
			"Only the attributes you set are managed. Anything you leave out (and every option this resource does not " +
			"model, such as `pppoe`, `poe` or `bridge-group`) is neither read nor changed. VLAN subinterfaces are " +
			"managed with `edgeos_interface_vif` and are never touched. Removing an attribute from the configuration " +
			"removes that setting from the port. Import reads every modelled setting the port has.\n\n" +
			"Destroying the resource removes the settings it manages from the port and keeps the port. `duplex` and " +
			"`speed` cannot be removed from a physical port, so destroy sets both back to `auto`. Removing `addresses` " +
			"or `firewall_*` from a port you reach the router through can cut you off.",
		Attributes: attrs,
	}
}

func ethernetToLeaves(ctx context.Context, m *ethernetModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putString(l, m.Duplex, "duplex")
	putString(l, m.Speed, "speed")
	ifaceToLeaves(ctx, l, &m.ifaceSettings, diags)
	return l
}

func ethernetFromTree(_ context.Context, tree any, m *ethernetModel, diags *diag.Diagnostics) {
	// The ID is null only right after an import, which reads everything.
	m.Duplex = getString(tree, "duplex")
	m.Speed = getString(tree, "speed")
	ifaceFromTree(tree, &m.ifaceSettings, m.ID.IsNull(), diags)
}

func ethernetOwned(m *ethernetModel) []configtree.Path {
	// disabled = false still owns the disable node, so a disabled port is
	// switched on.
	if known(m.Disabled) && !m.Disabled.ValueBool() {
		return []configtree.Path{configtree.NewPath("disable")}
	}
	return nil
}

func ethernetReset(*ethernetModel) configtree.Leaves {
	l := configtree.NewLeaves()
	l.SetSingle(configtree.NewPath("duplex"), "auto")
	l.SetSingle(configtree.NewPath("speed"), "auto")
	return l
}

func validateEthernet(_ context.Context, m *ethernetModel, diags *diag.Diagnostics) {
	validateIface(&m.ifaceSettings, diags)
	if m.Duplex.IsUnknown() || m.Speed.IsUnknown() {
		return
	}
	if m.Duplex.IsNull() != m.Speed.IsNull() {
		diags.AddAttributeError(path.Root("speed"), "Set duplex and speed together",
			"EdgeOS needs duplex and speed to be both auto or both fixed, so set both or neither.")
		return
	}
	if known(m.Duplex) && (m.Duplex.ValueString() == "auto") != (m.Speed.ValueString() == "auto") {
		diags.AddAttributeError(path.Root("speed"), "Mixed auto and fixed link settings",
			fmt.Sprintf("duplex = %q and speed = %q: EdgeOS needs both to be auto or both to be fixed.",
				m.Duplex.ValueString(), m.Speed.ValueString()))
	}
}

func ethernetImport(id string) (map[string]attr.Value, error) {
	if !ethernetRE.MatchString(id) {
		return nil, fmt.Errorf("%q is not an ethernet interface name", id)
	}
	return map[string]attr.Value{"name": types.StringValue(id)}, nil
}

func newInterfaceEthernetResource() resourceFactory {
	return newLeafResource(&leafSpec[ethernetModel]{
		Name:   "interface_ethernet",
		Schema: ethernetSchema(),
		Path: func(m *ethernetModel) configtree.Path {
			return configtree.NewPath("interfaces", "ethernet", m.Name.ValueString())
		},
		ID:          func(m *ethernetModel) string { return m.Name.ValueString() },
		SetID:       func(m *ethernetModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    ethernetToLeaves,
		FromTree:    ethernetFromTree,
		ImportAttrs: ethernetImport,
		ImportHint:  "the interface name, for example eth0",
		MustExist:   true,
		Owned:       ethernetOwned,
		Reset:       ethernetReset,
		Validate:    validateEthernet,
	})
}

// edgeos_interface_vif

type vifModel struct {
	ID        types.String `tfsdk:"id"`
	Interface types.String `tfsdk:"interface"`
	VLAN      types.Int64  `tfsdk:"vlan"`
	ifaceSettings
}

func vifSchema() schema.Schema {
	attrs := ifaceAttributes("VLAN interface", disabledAttribute("VLAN interface"))
	attrs["id"] = idAttribute("`<interface>.<vlan>`, such as `eth0.100`.")
	attrs["interface"] = schema.StringAttribute{
		MarkdownDescription: "The parent ethernet port, such as `eth0`. Changing it creates a new VLAN interface.",
		Required:            true,
		Validators:          []validator.String{ethernetName()},
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attrs["vlan"] = schema.Int64Attribute{
		MarkdownDescription: "VLAN ID from 1 to 4094. Changing it creates a new VLAN interface.",
		Required:            true,
		Validators:          []validator.Int64{int64validator.Between(1, 4094)},
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
	}
	return schema.Schema{
		MarkdownDescription: "An 802.1Q VLAN subinterface of an ethernet port (`interfaces ethernet <interface> vif <vlan>`).\n\n" +
			"The resource owns the VLAN interface: settings changed outside Terraform show up as drift. Options this " +
			"resource does not model (for example `egress-qos` or `dhcpv6-pd`) are left alone on update.\n\n" +
			"Destroying the resource deletes the whole VLAN interface, including options Terraform does not manage. " +
			"The parent port is never changed.",
		Attributes: attrs,
	}
}

func vifToLeaves(ctx context.Context, m *vifModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	ifaceToLeaves(ctx, l, &m.ifaceSettings, diags)
	return l
}

func vifFromTree(_ context.Context, tree any, m *vifModel, diags *diag.Diagnostics) {
	ifaceFromTree(tree, &m.ifaceSettings, true, diags)
}

func vifID(m *vifModel) string {
	return fmt.Sprintf("%s.%d", m.Interface.ValueString(), m.VLAN.ValueInt64())
}

func vifImport(id string) (map[string]attr.Value, error) {
	parent, vlan, ok := strings.Cut(id, ".")
	if !ok {
		return nil, fmt.Errorf("missing '.'")
	}
	if !ethernetRE.MatchString(parent) {
		return nil, fmt.Errorf("%q is not an ethernet interface name", parent)
	}
	n, err := strconv.ParseInt(vlan, 10, 64)
	if err != nil || n < 1 || n > 4094 || strconv.FormatInt(n, 10) != vlan {
		return nil, fmt.Errorf("%q is not a VLAN ID from 1 to 4094", vlan)
	}
	return map[string]attr.Value{"interface": types.StringValue(parent), "vlan": types.Int64Value(n)}, nil
}

func newInterfaceVifResource() resourceFactory {
	return newLeafResource(&leafSpec[vifModel]{
		Name:   "interface_vif",
		Schema: vifSchema(),
		Path: func(m *vifModel) configtree.Path {
			return configtree.NewPath("interfaces", "ethernet", m.Interface.ValueString(), "vif", strconv.FormatInt(m.VLAN.ValueInt64(), 10))
		},
		ID:          vifID,
		SetID:       func(m *vifModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    vifToLeaves,
		FromTree:    vifFromTree,
		ImportAttrs: vifImport,
		ImportHint:  "<interface>.<vlan>, for example eth0.100",
		Validate:    func(_ context.Context, m *vifModel, diags *diag.Diagnostics) { validateIface(&m.ifaceSettings, diags) },
	})
}
