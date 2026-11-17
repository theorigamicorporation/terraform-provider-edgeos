// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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

// secretFromDevice picks the value for a secret the router may hide. The web
// API replaces sensitive values with a placeholder for sessions that may not
// see them, and older firmware leaves them out. Anything that is neither a
// WireGuard key nor a file path is treated as hidden, and the prior value is
// kept.
func secretFromDevice(fromDevice, prior types.String) types.String {
	if known(fromDevice) {
		s := fromDevice.ValueString()
		if isWGKey(s) || isKeyFile(s) {
			return fromDevice
		}
	}
	return prior
}

// publicKeyOf returns the public key for an inline private key, or null for a
// key file and for null values.
func publicKeyOf(priv types.String) types.String {
	if priv.IsUnknown() {
		return types.StringUnknown()
	}
	if pub, ok := wgPublicKey(priv.ValueString()); ok {
		return types.StringValue(pub)
	}
	return types.StringNull()
}

// publicKeyFromPrivate computes public_key at plan time so plans show the
// real value instead of "known after apply".
type publicKeyFromPrivate struct{}

func (publicKeyFromPrivate) Description(context.Context) string {
	return "derived from private_key"
}

func (m publicKeyFromPrivate) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (publicKeyFromPrivate) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	// Plan modifiers all see the plan before any of them ran, so work out
	// what keepDeviceSecret plans for private_key instead of reading it.
	var priv, prior types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("private_key"), &priv)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("private_key"), &prior)...)
	}
	resp.PlanValue = publicKeyOf(keptSecret(priv, prior, !req.State.Raw.IsNull()))
}

// keptSecret is the planned value of a secret the router may hold as a key
// file path: the configured value, or, when the attribute is left out, the
// value in state, so leaving it out keeps whatever the router has.
func keptSecret(config, state types.String, exists bool) types.String {
	if !config.IsNull() || !exists {
		return config
	}
	return state
}

// keepDeviceSecret plans private_key and preshared_key. Both are Optional and
// Computed so that an imported resource, whose generated config has them as
// null, plans no change: a null in config keeps the value in state, which
// Read takes from the router. With required, a resource that is created (or
// replaced) without a value is an error.
type keepDeviceSecret struct{ required bool }

func (keepDeviceSecret) Description(context.Context) string {
	return "keeps the value on the router when not configured"
}

func (m keepDeviceSecret) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (m keepDeviceSecret) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.Plan.Raw.IsNull() || !req.ConfigValue.IsNull() {
		return
	}
	exists := !req.State.Raw.IsNull()
	if !exists && m.required {
		resp.Diagnostics.AddAttributeError(req.Path, "Missing private key",
			"private_key must be set when the interface is created or replaced: a base64 key or the path of a key file "+
				"on the router, such as /config/auth/wg0.key. It may be left out of the configuration of an existing "+
				"interface, which then keeps the key it has.")
		return
	}
	resp.PlanValue = keptSecret(req.ConfigValue, req.StateValue, exists)
}

// WireGuard interface

type wireguardInterfaceModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Addresses       types.Set    `tfsdk:"addresses"`
	Description     types.String `tfsdk:"description"`
	Disabled        types.Bool   `tfsdk:"disabled"`
	ListenPort      types.Int64  `tfsdk:"listen_port"`
	MTU             types.Int64  `tfsdk:"mtu"`
	PrivateKey      types.String `tfsdk:"private_key"`
	PublicKey       types.String `tfsdk:"public_key"`
	RouteAllowedIPs types.Bool   `tfsdk:"route_allowed_ips"`
	FWMark          types.Int64  `tfsdk:"fwmark"`
	FirewallIn      types.String `tfsdk:"firewall_in"`
	FirewallOut     types.String `tfsdk:"firewall_out"`
	FirewallLocal   types.String `tfsdk:"firewall_local"`
}

func firewallRef(dir string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: fmt.Sprintf("IPv4 firewall rule set applied to %s (`firewall %s name`), usually `edgeos_firewall_name.x.name`.",
			map[string]string{"in": "traffic coming in from the tunnel", "out": "traffic going out to the tunnel",
				"local": "traffic from the tunnel to the router itself"}[dir], dir),
		Optional:   true,
		Validators: []validator.String{firewallName()},
	}
}

func newWireguardInterfaceResource() resourceFactory {
	return newLeafResource(&leafSpec[wireguardInterfaceModel]{
		Name: "wireguard_interface",
		Schema: schema.Schema{
			MarkdownDescription: "A WireGuard interface (`interfaces wireguard <name>`). Needs the WireGuard package " +
				"for EdgeOS on the router.\n\n" +
				"Peers are managed with `edgeos_wireguard_peer`; this resource never touches them on update. " +
				"Destroying the interface deletes the whole node, including every peer, managed by Terraform or not.\n\n" +
				"Prefer a key file for `private_key` (`/config/auth/wg0.key`, created with `wg genkey` on the router), " +
				"so the private key never ends up in the Terraform state. An inline key works too but is stored in the state.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The interface name."),
				"name": schema.StringAttribute{
					MarkdownDescription: "Interface name from `wg0` to `wg999`. Changing it creates a new interface.",
					Required:            true,
					Validators:          []validator.String{wireguardName()},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"addresses": schema.SetAttribute{
					MarkdownDescription: "Addresses of the interface with prefix length, such as `10.8.0.1/24` or `fd00:8::1/64`.",
					Optional:            true,
					ElementType:         types.StringType,
					Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(ipPrefix())},
				},
				"description": descriptionAttribute(),
				"disabled":    disabledAttribute("interface"),
				"listen_port": schema.Int64Attribute{
					MarkdownDescription: "UDP port to listen on. Unset picks a random port, which only suits interfaces that never accept incoming handshakes.",
					Optional:            true,
					Validators:          []validator.Int64{int64validator.Between(1, 65535)},
				},
				"mtu": schema.Int64Attribute{
					MarkdownDescription: "MTU from 68 to 9000. WireGuard uses 1420 when unset.",
					Optional:            true,
					Validators:          []validator.Int64{int64validator.Between(68, 9000)},
				},
				"private_key": schema.StringAttribute{
					MarkdownDescription: "Private key of the interface: either the base64 key itself or an absolute path " +
						"to a file on the router that holds it, such as `/config/auth/wg0.key`. Files outside `/config` " +
						"are lost on a firmware upgrade. " +
						"Required when the interface is created or replaced. On an existing interface it may be left " +
						"out (as in config generated by an `import` block): the interface then keeps the key the " +
						"router has and the state follows the router. " +
						"If the router hides the value from the API session, the configured value is kept in state, " +
						"so a key changed on the router behind Terraform's back is then not detected.",
					Optional:      true,
					Computed:      true,
					Sensitive:     true,
					Validators:    []validator.String{wireguardKeyOrFile()},
					PlanModifiers: []planmodifier.String{keepDeviceSecret{required: true}},
				},
				"public_key": schema.StringAttribute{
					MarkdownDescription: "Public key derived from an inline `private_key`, for the peers' configs. " +
						"Null when `private_key` is a file path; run `wg pubkey < /config/auth/wg0.key` on the router instead.",
					Computed:      true,
					PlanModifiers: []planmodifier.String{publicKeyFromPrivate{}},
				},
				"route_allowed_ips": schema.BoolAttribute{
					MarkdownDescription: "Add routes for the peers' `allowed_ips` through this interface. Unset leaves the " +
						"EdgeOS default (`true`).",
					Optional: true,
				},
				"fwmark": schema.Int64Attribute{
					MarkdownDescription: "Firewall mark set on the interface's outgoing packets, for policy routing.",
					Optional:            true,
					Validators:          []validator.Int64{int64validator.Between(0, 4294967295)},
				},
				"firewall_in":    firewallRef("in"),
				"firewall_out":   firewallRef("out"),
				"firewall_local": firewallRef("local"),
			},
		},
		Path: func(m *wireguardInterfaceModel) configtree.Path {
			return configtree.NewPath("interfaces", "wireguard", m.Name.ValueString())
		},
		ID:       func(m *wireguardInterfaceModel) string { return m.Name.ValueString() },
		SetID:    func(m *wireguardInterfaceModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: wireguardInterfaceToLeaves,
		FromTree: wireguardInterfaceFromTree,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if !wireguardRE.MatchString(id) {
				return nil, fmt.Errorf("%q is not a WireGuard interface name", id)
			}
			return map[string]attr.Value{"name": types.StringValue(id)}, nil
		},
		ImportHint: "the interface name, for example wg0",
	})
}

func wireguardInterfaceToLeaves(ctx context.Context, m *wireguardInterfaceModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putStrings(ctx, l, m.Addresses, diags, "address")
	putString(l, m.Description, "description")
	putFlag(l, m.Disabled, "disable")
	putInt(l, m.ListenPort, "listen-port")
	putInt(l, m.MTU, "mtu")
	putString(l, m.PrivateKey, "private-key")
	putBoolString(l, m.RouteAllowedIPs, "route-allowed-ips")
	putInt(l, m.FWMark, "fwmark")
	putString(l, m.FirewallIn, "firewall", "in", "name")
	putString(l, m.FirewallOut, "firewall", "out", "name")
	putString(l, m.FirewallLocal, "firewall", "local", "name")
	return l
}

func wireguardInterfaceFromTree(_ context.Context, tree any, m *wireguardInterfaceModel, diags *diag.Diagnostics) {
	m.Addresses = getSet(tree, "address")
	m.Description = getString(tree, "description")
	m.Disabled = getFlag(tree, "disable")
	m.ListenPort = getInt(tree, diags, "listen-port")
	m.MTU = getInt(tree, diags, "mtu")
	m.PrivateKey = secretFromDevice(getString(tree, "private-key"), m.PrivateKey)
	m.PublicKey = publicKeyOf(m.PrivateKey)
	m.RouteAllowedIPs = getBoolString(tree, "route-allowed-ips")
	m.FWMark = getInt(tree, diags, "fwmark")
	m.FirewallIn = getString(tree, "firewall", "in", "name")
	m.FirewallOut = getString(tree, "firewall", "out", "name")
	m.FirewallLocal = getString(tree, "firewall", "local", "name")
}

// WireGuard peer

type wireguardPeerModel struct {
	ID                  types.String `tfsdk:"id"`
	Interface           types.String `tfsdk:"interface"`
	PublicKey           types.String `tfsdk:"public_key"`
	AllowedIPs          types.Set    `tfsdk:"allowed_ips"`
	Description         types.String `tfsdk:"description"`
	Disabled            types.Bool   `tfsdk:"disabled"`
	Endpoint            types.String `tfsdk:"endpoint"`
	PersistentKeepalive types.Int64  `tfsdk:"persistent_keepalive"`
	PresharedKey        types.String `tfsdk:"preshared_key"`
}

func newWireguardPeerResource() resourceFactory {
	return newLeafResource(&leafSpec[wireguardPeerModel]{
		Name: "wireguard_peer",
		Schema: schema.Schema{
			MarkdownDescription: "A peer of a WireGuard interface (`interfaces wireguard <interface> peer <public_key>`). " +
				"Destroying the resource deletes the peer node.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("`<interface>:<public_key>`, for example `wg0:<base64 key>`."),
				"interface": schema.StringAttribute{
					MarkdownDescription: "WireGuard interface, usually `edgeos_wireguard_interface.x.name`. Changing it creates a new peer.",
					Required:            true,
					Validators:          []validator.String{wireguardName()},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"public_key": schema.StringAttribute{
					MarkdownDescription: "The peer's base64 public key. It names the peer on the router, so changing it creates a new peer.",
					Required:            true,
					Validators:          []validator.String{wireguardKey()},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"allowed_ips": schema.SetAttribute{
					MarkdownDescription: "Networks the peer may send from and that are routed to it, such as `10.8.0.2/32` or `0.0.0.0/0`.",
					Required:            true,
					ElementType:         types.StringType,
					Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(ipPrefix())},
				},
				"description": descriptionAttribute(),
				"disabled":    disabledAttribute("peer"),
				"endpoint": schema.StringAttribute{
					MarkdownDescription: "Where to reach the peer, as `host:port`, such as `vpn.example.com:51820`, " +
						"`203.0.113.10:51820` or `[2001:db8::1]:51820`. Leave unset for road warriors that connect to the router.",
					Optional:   true,
					Validators: []validator.String{wireguardEndpoint()},
				},
				"persistent_keepalive": schema.Int64Attribute{
					MarkdownDescription: "Send a keepalive every this many seconds, which keeps NAT mappings open. 25 is a common value.",
					Optional:            true,
					Validators:          []validator.Int64{int64validator.Between(1, 65535)},
				},
				"preshared_key": schema.StringAttribute{
					MarkdownDescription: "Optional preshared key for extra protection: the base64 key or an absolute path " +
						"to a file on the router, such as `/config/auth/wg0-laptop.psk`. " +
						"Leaving it out keeps whatever the router has, so an imported peer keeps its preshared key " +
						"(and a new peer gets none). Set it to `\"\"` to remove the preshared key from the peer. " +
						"If the router hides the value from the API session, the configured value is kept in state.",
					Optional:      true,
					Computed:      true,
					Sensitive:     true,
					Validators:    []validator.String{wireguardKeyFileOrEmpty()},
					PlanModifiers: []planmodifier.String{keepDeviceSecret{}},
				},
			},
		},
		Path: func(m *wireguardPeerModel) configtree.Path {
			return configtree.NewPath("interfaces", "wireguard", m.Interface.ValueString(), "peer", m.PublicKey.ValueString())
		},
		ID: func(m *wireguardPeerModel) string {
			return m.Interface.ValueString() + ":" + m.PublicKey.ValueString()
		},
		SetID:       func(m *wireguardPeerModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    wireguardPeerToLeaves,
		FromTree:    wireguardPeerFromTree,
		ImportAttrs: wireguardPeerImport,
		ImportHint:  "<interface>:<public_key>, for example wg0:<base64 public key>",
	})
}

func wireguardPeerToLeaves(ctx context.Context, m *wireguardPeerModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putStrings(ctx, l, m.AllowedIPs, diags, "allowed-ips")
	putString(l, m.Description, "description")
	putFlag(l, m.Disabled, "disable")
	putString(l, m.Endpoint, "endpoint")
	putInt(l, m.PersistentKeepalive, "persistent-keepalive")
	if m.PresharedKey.ValueString() != "" {
		// "" stands for no preshared key.
		putString(l, m.PresharedKey, "preshared-key")
	}
	return l
}

func wireguardPeerFromTree(_ context.Context, tree any, m *wireguardPeerModel, diags *diag.Diagnostics) {
	m.AllowedIPs = getSet(tree, "allowed-ips")
	m.Description = getString(tree, "description")
	m.Disabled = getFlag(tree, "disable")
	m.Endpoint = getString(tree, "endpoint")
	m.PersistentKeepalive = getInt(tree, diags, "persistent-keepalive")
	psk := getString(tree, "preshared-key")
	switch {
	case psk.IsNull() && known(m.PresharedKey) && m.PresharedKey.ValueString() == "":
		// Configured as "", which stands for none.
	case psk.IsNull():
		// Absent on the router means no preshared key, unless the router
		// hides it; a hidden value shows up as a placeholder instead.
		m.PresharedKey = types.StringNull()
	default:
		m.PresharedKey = secretFromDevice(psk, m.PresharedKey)
	}
}

func wireguardPeerImport(id string) (map[string]attr.Value, error) {
	iface, key, ok := strings.Cut(id, ":")
	if !ok {
		return nil, fmt.Errorf("missing ':'")
	}
	if !wireguardRE.MatchString(iface) {
		return nil, fmt.Errorf("%q is not a WireGuard interface name", iface)
	}
	if _, err := parseWGKey(key); err != nil {
		return nil, fmt.Errorf("%q is not a public key: %w", key, err)
	}
	return map[string]attr.Value{"interface": types.StringValue(iface), "public_key": types.StringValue(key)}, nil
}
