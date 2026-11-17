// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func nullIface() ifaceSettings {
	return ifaceSettings{
		Description: types.StringNull(), Addresses: types.SetNull(types.StringType), MTU: types.Int64Null(),
		MAC: types.StringNull(), Disabled: types.BoolNull(), FirewallIn: types.StringNull(),
		FirewallOut: types.StringNull(), FirewallLocal: types.StringNull(),
	}
}

func fullIface() ifaceSettings {
	return ifaceSettings{
		Description:   types.StringValue("ISP WAN"),
		Addresses:     stringSet([]string{"dhcp", "203.0.113.2/30"}),
		MTU:           types.Int64Value(1492),
		MAC:           types.StringValue("02:00:5e:00:53:01"),
		Disabled:      types.BoolValue(true),
		FirewallIn:    types.StringValue("WAN_IN"),
		FirewallOut:   types.StringValue("WAN_OUT"),
		FirewallLocal: types.StringValue("WAN_LOCAL"),
		DHCPOptions: &dhcpOptionsModel{
			DefaultRoute: types.BoolValue(true), DefaultRouteDistance: types.Int64Value(210), NameServer: types.BoolValue(false),
			ClientOptions: stringList([]string{"send dhcp-client-identifier 01:02:00:5e:00:53:01;", "send vendor-class-identifier 75:64:68:63:70:20:31:2e:32:35:2e:31;"}),
			GlobalOptions: stringList([]string{"option rfc3442-classless-static-routes code 121 = array of unsigned integer 8;"}),
		},
	}
}

var fullIfaceLeaves = map[string]configtree.Value{
	"description":                         configtree.Single("ISP WAN"),
	"address":                             configtree.UnorderedMulti("203.0.113.2/30", "dhcp"),
	"mtu":                                 configtree.Single("1492"),
	"mac":                                 configtree.Single("02:00:5e:00:53:01"),
	"disable":                             configtree.Flag(),
	"firewall in name":                    configtree.Single("WAN_IN"),
	"firewall out name":                   configtree.Single("WAN_OUT"),
	"firewall local name":                 configtree.Single("WAN_LOCAL"),
	"dhcp-options default-route":          configtree.Single("update"),
	"dhcp-options default-route-distance": configtree.Single("210"),
	"dhcp-options name-server":            configtree.Single("no-update"),
	"dhcp-options client-option": configtree.Multi("send dhcp-client-identifier 01:02:00:5e:00:53:01;",
		"send vendor-class-identifier 75:64:68:63:70:20:31:2e:32:35:2e:31;"),
	"dhcp-options global-option": configtree.Multi("option rfc3442-classless-static-routes code 121 = array of unsigned integer 8;"),
}

func withLeaves(base map[string]configtree.Value, extra map[string]configtree.Value) map[string]configtree.Value {
	out := map[string]configtree.Value{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestEthernetMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		model  ethernetModel
		leaves map[string]configtree.Value
	}{
		{
			name:   "nothing managed",
			model:  ethernetModel{Name: types.StringValue("eth0"), Duplex: types.StringUnknown(), Speed: types.StringUnknown(), ifaceSettings: nullIface()},
			leaves: map[string]configtree.Value{},
		},
		{
			name:  "enabled port",
			model: ethernetModel{Name: types.StringValue("eth0"), Duplex: types.StringValue("auto"), Speed: types.StringValue("auto"), ifaceSettings: func() ifaceSettings { s := nullIface(); s.Disabled = types.BoolValue(false); return s }()},
			leaves: map[string]configtree.Value{
				"duplex": configtree.Single("auto"),
				"speed":  configtree.Single("auto"),
			},
		},
		{
			name:  "everything",
			model: ethernetModel{Name: types.StringValue("eth0"), Duplex: types.StringValue("full"), Speed: types.StringValue("1000"), ifaceSettings: fullIface()},
			leaves: withLeaves(fullIfaceLeaves, map[string]configtree.Value{
				"duplex": configtree.Single("full"),
				"speed":  configtree.Single("1000"),
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := ethernetToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)

			// Round trip through the tree the router would store, as an import.
			back := ethernetModel{ID: types.StringNull(), ifaceSettings: nullIface()}
			ethernetFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			if !tc.model.Disabled.IsNull() && back.Disabled.ValueBool() != tc.model.Disabled.ValueBool() {
				t.Errorf("disabled = %v", back.Disabled)
			}
			assertLeaves(t, ethernetToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}
}

func TestEthernetFromTreeReadsOnlyManagedAttributes(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	tree := map[string]any{
		"address": []any{"192.168.1.1/24"}, "description": "LAN", "duplex": "auto", "speed": "auto",
		"mtu": "1500", "disable": nil, "firewall": map[string]any{"in": map[string]any{"name": "LAN_IN"}},
		"dhcp-options": map[string]any{"default-route": "no-update", "name-server": "update"},
		"vif":          map[string]any{"10": map[string]any{"description": "not mine"}},
	}
	m := ethernetModel{ID: types.StringValue("eth1"), Name: types.StringValue("eth1"), ifaceSettings: nullIface()}
	m.Description = types.StringValue("old")
	m.DHCPOptions = &dhcpOptionsModel{DefaultRoute: types.BoolValue(true), DefaultRouteDistance: types.Int64Null(), NameServer: types.BoolNull()}
	ethernetFromTree(context.Background(), tree, &m, &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if m.Description.ValueString() != "LAN" || m.Duplex.ValueString() != "auto" || m.Speed.ValueString() != "auto" {
		t.Errorf("managed attributes not read: %+v", m)
	}
	if !m.Addresses.IsNull() || !m.MTU.IsNull() || !m.Disabled.IsNull() || !m.FirewallIn.IsNull() {
		t.Errorf("unmanaged attributes were read: %+v", m)
	}
	if m.DHCPOptions == nil || m.DHCPOptions.DefaultRoute.ValueBool() || !m.DHCPOptions.NameServer.IsNull() {
		t.Errorf("dhcp options = %+v", m.DHCPOptions)
	}

	// A managed dhcp option that is gone on the router drops the block.
	delete(tree, "dhcp-options")
	ethernetFromTree(context.Background(), tree, &m, &diags)
	if m.DHCPOptions != nil {
		t.Errorf("dhcp options should be nil, got %+v", m.DHCPOptions)
	}

	// A bad number is reported.
	m.MTU = types.Int64Value(1500)
	tree["mtu"] = "jumbo"
	ethernetFromTree(context.Background(), tree, &m, &diags)
	if !diags.HasError() {
		t.Error("a non-numeric MTU should be reported")
	}
}

func TestEthernetOwnedAndReset(t *testing.T) {
	t.Parallel()
	m := ethernetModel{ifaceSettings: nullIface()}
	if got := ethernetOwned(&m); len(got) != 0 {
		t.Errorf("unset disabled owns %v", got)
	}
	m.Disabled = types.BoolValue(true)
	if got := ethernetOwned(&m); len(got) != 0 {
		t.Errorf("disabled = true owns %v (ToLeaves covers it)", got)
	}
	m.Disabled = types.BoolValue(false)
	if got := ethernetOwned(&m); len(got) != 1 || got[0].String() != "disable" {
		t.Errorf("disabled = false owns %v", got)
	}
	assertLeaves(t, ethernetReset(&m), map[string]configtree.Value{
		"duplex": configtree.Single("auto"), "speed": configtree.Single("auto"),
	})
}

func TestValidateEthernet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		duplex, speed types.String
		want          string
	}{
		{types.StringNull(), types.StringNull(), ""},
		{types.StringValue("auto"), types.StringValue("auto"), ""},
		{types.StringValue("full"), types.StringValue("1000"), ""},
		{types.StringUnknown(), types.StringValue("1000"), ""},
		{types.StringValue("full"), types.StringNull(), "Set duplex and speed together"},
		{types.StringValue("auto"), types.StringValue("100"), "Mixed auto and fixed"},
		{types.StringValue("half"), types.StringValue("auto"), "Mixed auto and fixed"},
	}
	for _, tc := range cases {
		var diags diag.Diagnostics
		m := ethernetModel{Duplex: tc.duplex, Speed: tc.speed, ifaceSettings: nullIface()}
		validateEthernet(context.Background(), &m, &diags)
		got := ""
		if diags.HasError() {
			got = diags.Errors()[0].Summary()
		}
		if !regexp.MustCompile(tc.want).MatchString(got) || (tc.want == "" && got != "") {
			t.Errorf("duplex %v speed %v: got %q, want %q", tc.duplex, tc.speed, got, tc.want)
		}
	}
}

func TestVifMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	minimal := nullIface()
	minimal.Disabled = types.BoolValue(false)
	cases := []struct {
		name   string
		model  vifModel
		leaves map[string]configtree.Value
	}{
		{"minimal", vifModel{ifaceSettings: minimal}, map[string]configtree.Value{}},
		{"everything", vifModel{ifaceSettings: fullIface()}, fullIfaceLeaves},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := vifToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := vifModel{ifaceSettings: nullIface()}
			vifFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			if back.Disabled.IsNull() {
				t.Error("disabled must always be read for a VLAN interface")
			}
			assertLeaves(t, vifToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}
}

func TestVifFromRealGetJSON(t *testing.T) {
	t.Parallel()
	// Shape of a VLAN WAN as get.json returns it.
	tree := map[string]any{
		"address": []any{"dhcp"}, "description": "ISP WAN",
		"dhcp-options": map[string]any{"default-route": "update", "default-route-distance": "210", "name-server": "no-update"},
		"firewall":     map[string]any{"in": map[string]any{"name": "WAN_IN"}, "local": map[string]any{"name": "WAN_LOCAL"}},
		"egress-qos":   "0:0",
	}
	var diags diag.Diagnostics
	m := vifModel{ifaceSettings: nullIface()}
	vifFromTree(context.Background(), tree, &m, &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if m.Description.ValueString() != "ISP WAN" || m.FirewallIn.ValueString() != "WAN_IN" || m.FirewallLocal.ValueString() != "WAN_LOCAL" ||
		!m.FirewallOut.IsNull() || m.Disabled.ValueBool() || !m.MTU.IsNull() {
		t.Errorf("unexpected model %+v", m)
	}
	d := m.DHCPOptions
	if d == nil || !d.DefaultRoute.ValueBool() || d.DefaultRouteDistance.ValueInt64() != 210 || d.NameServer.ValueBool() {
		t.Errorf("dhcp options = %+v", d)
	}
	if !m.Addresses.Equal(stringSet([]string{"dhcp"})) {
		t.Errorf("addresses = %v", m.Addresses)
	}
}

func TestInterfaceImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{"eth0": true, "eth12": true, "eth": false, "switch0": false, "eth0.10": false, "": false} {
		if _, err := ethernetImport(id); (err == nil) != ok {
			t.Errorf("ethernet %q: err = %v", id, err)
		}
	}
	for id, ok := range map[string]bool{
		"eth0.100": true, "eth3.1": true, "eth3.4094": true, "eth0": false, "eth0.0": false, "eth0.4095": false,
		"eth0.x": false, "switch0.10": false, "eth0.010": false, "eth0.10.20": false,
	} {
		attrs, err := vifImport(id)
		if (err == nil) != ok {
			t.Errorf("vif %q: err = %v", id, err)
		}
		if ok && vifID(&vifModel{Interface: attrs["interface"].(types.String), VLAN: attrs["vlan"].(types.Int64)}) != id {
			t.Errorf("vif %q does not round trip: %v", id, attrs)
		}
	}
}

func TestSwitchMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		model  switchModel
		leaves map[string]configtree.Value
	}{
		{
			name:   "nothing managed",
			model:  switchModel{Name: types.StringValue("switch0"), SwitchPorts: types.SetNull(types.StringType), VLANAware: types.BoolNull(), ifaceSettings: nullIface()},
			leaves: map[string]configtree.Value{},
		},
		{
			name:  "everything",
			model: switchModel{Name: types.StringValue("switch0"), SwitchPorts: stringSet([]string{"eth3", "eth2"}), VLANAware: types.BoolValue(false), ifaceSettings: fullIface()},
			leaves: withLeaves(fullIfaceLeaves, map[string]configtree.Value{
				"switch-port interface eth2": configtree.Flag(),
				"switch-port interface eth3": configtree.Flag(),
				"switch-port vlan-aware":     configtree.Single("disable"),
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := switchToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)

			// Round trip as an import, which reads everything.
			back := switchModel{ID: types.StringNull(), SwitchPorts: types.SetNull(types.StringType), VLANAware: types.BoolNull(), ifaceSettings: nullIface()}
			switchFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, switchToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}
}

func TestSwitchFromTreeReadsOnlyManagedAttributes(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	tree := map[string]any{
		"address": []any{"192.168.1.1/24"}, "description": "LAN", "mtu": "1500",
		"switch-port": map[string]any{
			"interface":  map[string]any{"eth2": nil, "eth3": map[string]any{"vlan": map[string]any{"pvid": "10"}}},
			"vlan-aware": "enable",
		},
		"dhcp-options": map[string]any{"client-option": []any{"send host-name lan;"}, "global-option": []any{"option x code 77 = text;"}},
	}
	m := switchModel{ID: types.StringValue("switch0"), Name: types.StringValue("switch0"),
		SwitchPorts: stringSet([]string{"eth2"}), VLANAware: types.BoolNull(), ifaceSettings: nullIface()}
	m.DHCPOptions = &dhcpOptionsModel{DefaultRoute: types.BoolNull(), DefaultRouteDistance: types.Int64Null(), NameServer: types.BoolNull(),
		ClientOptions: stringList([]string{"old;"}), GlobalOptions: types.ListNull(types.StringType)}
	switchFromTree(context.Background(), tree, &m, &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if !m.SwitchPorts.Equal(stringSet([]string{"eth2", "eth3"})) {
		t.Errorf("switch ports = %v", m.SwitchPorts)
	}
	if !m.VLANAware.IsNull() || !m.Addresses.IsNull() || !m.MTU.IsNull() || !m.Description.IsNull() {
		t.Errorf("unmanaged attributes were read: %+v", m)
	}
	if d := m.DHCPOptions; d == nil || !d.ClientOptions.Equal(stringList([]string{"send host-name lan;"})) || !d.GlobalOptions.IsNull() {
		t.Errorf("dhcp options = %+v", m.DHCPOptions)
	}
}

func TestSwitchOwned(t *testing.T) {
	t.Parallel()
	m := switchModel{SwitchPorts: types.SetNull(types.StringType), ifaceSettings: nullIface()}
	if got := switchOwned(&m); len(got) != 0 {
		t.Errorf("nothing set owns %v", got)
	}
	m.SwitchPorts = stringSet([]string{"eth2"})
	m.Disabled = types.BoolValue(false)
	got := switchOwned(&m)
	if len(got) != 2 || got[0].String() != `switch-port interface "*"` || got[1].String() != "disable" {
		t.Errorf("owned = %v", got)
	}
}

func TestSwitchImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{"switch0": true, "switch12": true, "switch": false, "eth0": false, "switch0.10": false, "": false} {
		attrs, err := switchImport(id)
		if (err == nil) != ok {
			t.Errorf("switch %q: err = %v", id, err)
		}
		if ok && attrs["name"].(types.String).ValueString() != id {
			t.Errorf("switch %q: attrs = %v", id, attrs)
		}
	}
}

func TestDHCPClientOptionValidator(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]string{
		"send dhcp-client-identifier 01:a2:03:8e:03:ce:ff;":                 "",
		"send vendor-class-identifier 75:64:68:63:70:20:31:2e:32:35:2e:31;": "",
		`send vendor-class-identifier "udhcp 1.25.1";`:                      "Double quotes",
		"   ":                     "Empty DHCP option",
		"send host-name x;\nfoo;": "Invalid EdgeOS value",
	} {
		resp := &validator.StringResponse{}
		dhcpClientOption().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(s)}, resp)
		got := ""
		if resp.Diagnostics.HasError() {
			got = resp.Diagnostics.Errors()[0].Summary()
			if want == "Double quotes" && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "75:64:68:63:70:20:31:2e:32:35:2e:31") {
				t.Errorf("%q: the error should show the hex form: %s", s, resp.Diagnostics.Errors()[0].Detail())
			}
		}
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%q: got %q, want %q", s, got, want)
		}
	}
	resp := &validator.StringResponse{}
	dhcpClientOption().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringUnknown()}, resp)
	if resp.Diagnostics.HasError() || dhcpClientOption().MarkdownDescription(context.Background()) == "" {
		t.Error("unknown values must pass and the validator needs a description")
	}
}

func TestInterfaceAddressValidator(t *testing.T) {
	t.Parallel()
	for s, ok := range map[string]bool{
		"dhcp": true, "dhcpv6": true, "192.0.2.1/24": true, "2001:db8::1/64": true,
		"192.0.2.1": false, "dhcp4": false, "": false, "192.0.2.1/33": false,
	} {
		resp := &validator.StringResponse{}
		interfaceAddress().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(s)}, resp)
		if resp.Diagnostics.HasError() == ok {
			t.Errorf("%q: %v", s, resp.Diagnostics)
		}
	}
	resp := &validator.StringResponse{}
	interfaceAddress().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringUnknown()}, resp)
	if resp.Diagnostics.HasError() || interfaceAddress().MarkdownDescription(context.Background()) == "" {
		t.Error("unknown values must pass and the validator needs a description")
	}
}

// Acceptance tests against the fake router.

const ethFirewalls = `
resource "edgeos_firewall_name" "wan_in" {
  name = "WAN_IN"
}
resource "edgeos_firewall_name" "wan_local" {
  name = "WAN_LOCAL"
}
`

func TestAccInterfaceEthernet(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			// The port and its vif survive, the managed settings are gone
			// and duplex and speed are back to auto.
			e.checkValue("interfaces ethernet eth0", map[string]any{
				"duplex": "auto", "speed": "auto",
				"vif": map[string]any{"20": map[string]any{"description": "not managed"}},
			}),
			// eth1 kept its address, which was never managed.
			e.checkValue("interfaces ethernet eth1", map[string]any{
				"address": []string{"192.168.1.1/24"}, "duplex": "auto", "speed": "auto",
			}),
		),
		Steps: []resource.TestStep{
			{
				// Adopt eth0 with a few settings; leave duplex and speed alone.
				PreConfig: e.set("interfaces ethernet eth0 vif 20 description", configtree.Single("not managed")),
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_ethernet" "wan" {
  name           = "eth0"
  description    = "ISP uplink"
  addresses      = ["dhcp"]
  firewall_in    = edgeos_firewall_name.wan_in.name
  firewall_local = edgeos_firewall_name.wan_local.name
  dhcp_options = {
    default_route          = true
    default_route_distance = 210
    name_server            = false
  }
}

resource "edgeos_interface_ethernet" "lan" {
  name        = "eth1"
  description = "LAN"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_interface_ethernet.wan", "id", "eth0"),
					resource.TestCheckResourceAttr("edgeos_interface_ethernet.wan", "duplex", "auto"),
					resource.TestCheckResourceAttr("edgeos_interface_ethernet.wan", "speed", "auto"),
					resource.TestCheckNoResourceAttr("edgeos_interface_ethernet.wan", "disabled"),
					resource.TestCheckNoResourceAttr("edgeos_interface_ethernet.lan", "addresses"),
					e.checkValue("interfaces ethernet eth0", map[string]any{
						"address": []string{"dhcp"}, "description": "ISP uplink", "duplex": "auto", "speed": "auto",
						"firewall":     map[string]any{"in": map[string]any{"name": "WAN_IN"}, "local": map[string]any{"name": "WAN_LOCAL"}},
						"dhcp-options": map[string]any{"default-route": "update", "default-route-distance": "210", "name-server": "no-update"},
						"vif":          map[string]any{"20": map[string]any{"description": "not managed"}},
					}),
					e.checkValue("interfaces ethernet eth1 address", []string{"192.168.1.1/24"}),
					e.checkValue("interfaces ethernet eth1 description", "LAN"),
				),
			},
			{
				// Update in place: fixed link, static addresses, MTU, MAC,
				// disabled, no local firewall and fewer DHCP options.
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_ethernet" "wan" {
  name        = "eth0"
  description = "ISP uplink"
  addresses   = ["203.0.113.2/30", "2001:db8:100::2/64"]
  duplex      = "full"
  speed       = "1000"
  mtu         = 1500
  mac         = "02:00:5e:00:53:01"
  disabled    = true
  firewall_in = edgeos_firewall_name.wan_in.name
  dhcp_options = {
    default_route = false
  }
}

resource "edgeos_interface_ethernet" "lan" {
  name        = "eth1"
  description = "LAN"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_ethernet.wan", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_interface_ethernet.lan", plancheck.ResourceActionNoop),
				}},
				Check: e.checkValue("interfaces ethernet eth0", map[string]any{
					"address": []string{"2001:db8:100::2/64", "203.0.113.2/30"}, "description": "ISP uplink",
					"duplex": "full", "speed": "1000", "mtu": "1500", "mac": "02:00:5e:00:53:01", "disable": nil,
					"firewall":     map[string]any{"in": map[string]any{"name": "WAN_IN"}},
					"dhcp-options": map[string]any{"default-route": "no-update"},
					"vif":          map[string]any{"20": map[string]any{"description": "not managed"}},
				}),
			},
			{
				ResourceName:      "edgeos_interface_ethernet.wan",
				ImportState:       true,
				ImportStateId:     "eth0",
				ImportStateVerify: true,
			},
			{
				// Drift: the description and the MTU are changed by hand, and
				// the port is switched on again. Removing duplex and speed from
				// the config keeps the fixed link.
				PreConfig: func() {
					e.set("interfaces ethernet eth0 description", configtree.Single("hand edit"))()
					e.set("interfaces ethernet eth0 mtu", configtree.Single("9000"))()
					e.del("interfaces ethernet eth0 disable")()
				},
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_ethernet" "wan" {
  name        = "eth0"
  description = "ISP uplink"
  addresses   = ["203.0.113.2/30", "2001:db8:100::2/64"]
  mtu         = 1500
  mac         = "02:00:5e:00:53:01"
  disabled    = true
  firewall_in = edgeos_firewall_name.wan_in.name
  dhcp_options = {
    default_route = false
  }
}

resource "edgeos_interface_ethernet" "lan" {
  name        = "eth1"
  description = "LAN"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_ethernet.wan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces ethernet eth0 description", "ISP uplink"),
					e.checkValue("interfaces ethernet eth0 mtu", "1500"),
					e.checkValue("interfaces ethernet eth0 disable", nil),
					e.checkValue("interfaces ethernet eth0 duplex", "full"),
					e.checkValue("interfaces ethernet eth0 speed", "1000"),
				),
			},
			{
				// Back to auto negotiation; disabled = false switches the port
				// on; settings removed from the config are removed from the port.
				PreConfig: e.set("interfaces ethernet eth1 disable", configtree.Flag()),
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_ethernet" "wan" {
  name        = "eth0"
  description = "ISP uplink"
  addresses   = ["dhcp"]
  duplex      = "auto"
  speed       = "auto"
  disabled    = false
}

resource "edgeos_interface_ethernet" "lan" {
  name        = "eth1"
  description = "LAN"
  disabled    = false
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces ethernet eth0", map[string]any{
						"address": []string{"dhcp"}, "description": "ISP uplink", "duplex": "auto", "speed": "auto",
						"vif": map[string]any{"20": map[string]any{"description": "not managed"}},
					}),
					e.checkAbsent("interfaces ethernet eth1 disable"),
					resource.TestCheckResourceAttr("edgeos_interface_ethernet.lan", "disabled", "false"),
				),
			},
			{
				// A fixed link on the port at destroy time is put back to auto.
				Config: e.config(`
resource "edgeos_interface_ethernet" "wan" {
  name        = "eth0"
  description = "ISP uplink"
  addresses   = ["dhcp"]
  duplex      = "full"
  speed       = "100"
}

resource "edgeos_interface_ethernet" "lan" {
  name        = "eth1"
  description = "LAN"
}`),
				Check: e.checkValue("interfaces ethernet eth0 speed", "100"),
			},
		},
	})
}

func TestAccInterfaceEthernetReplace(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_interface_ethernet" "x" {
  name        = "eth2"
  description = "moving"
}`),
				Check: e.checkValue("interfaces ethernet eth2 description", "moving"),
			},
			{
				// Another port: the old one loses the description, both stay.
				Config: e.config(`
resource "edgeos_interface_ethernet" "x" {
  name        = "eth3"
  description = "moving"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_ethernet.x", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces ethernet eth2", map[string]any{"duplex": "auto", "speed": "auto"}),
					e.checkValue("interfaces ethernet eth3 description", "moving"),
				),
			},
		},
	})
}

func TestAccInterfaceEthernetErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// A port the router does not have.
				Config: e.config(`
resource "edgeos_interface_ethernet" "x" {
  name        = "eth9"
  description = "nope"
}`),
				ExpectError: regexp.MustCompile(`(?s)Not found.*interfaces ethernet eth9.*does not exist`),
			},
			{
				// Commit failure: nothing changes.
				PreConfig: func() { e.srv.FailNextCommit("eth2: link setup failed") },
				Config: e.config(`
resource "edgeos_interface_ethernet" "x" {
  name        = "eth2"
  description = "never"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*eth2: link setup failed`),
			},
			{
				// A firewall rule set that does not exist fails at commit.
				Config: e.config(`
resource "edgeos_interface_ethernet" "x" {
  name        = "eth2"
  firewall_in = "NO_SUCH_RULESET"
}`),
				ExpectError: regexp.MustCompile(`Firewall name \[NO_SUCH_RULESET\] does not exist`),
			},
		},
	})
	if got, ok := e.srv.Get(cliPath("interfaces ethernet eth2")); !ok || len(got.(map[string]any)) != 2 {
		t.Fatalf("eth2 changed after failed commits: %#v", got)
	}
}

func TestAccInterfaceValidation(t *testing.T) {
	e := newAccEnv(t)
	cases := []struct {
		res, body, want string
	}{
		{"ethernet", `name = "eth0"
  duplex = "full"`, `Set duplex and speed together`},
		{"ethernet", `name = "eth0"
  duplex = "auto"
  speed = "1000"`, `Mixed auto and fixed`},
		{"ethernet", `name = "eth0"
  speed = "2500"`, `value must be one of`},
		{"ethernet", `name = "switch0"`, `ethernet interface name`},
		{"ethernet", `name = "eth0"
  addresses = ["192.0.2.1"]`, `Invalid interface address`},
		{"ethernet", `name = "eth0"
  addresses = []`, `at least 1`},
		{"ethernet", `name = "eth0"
  mtu = 9001`, `between 68 and 9000`},
		{"ethernet", `name = "eth0"
  mac = "02:00:5E:00:53:01"`, `lower case MAC`},
		{"ethernet", `name = "eth0"
  description = "say \"hi\""`, `double quote`},
		{"ethernet", `name = "eth0"
  firewall_in = "this name is not allowed"`, `1 to 27 characters`},
		{"ethernet", `name = "eth0"
  dhcp_options = {}`, `Empty dhcp_options block`},
		{"ethernet", `name = "eth0"
  dhcp_options = { default_route_distance = 0 }`, `between 1 and 255`},
		{"vif", `interface = "eth0"
  vlan = 0`, `between 1 and 4094`},
		{"vif", `interface = "eth0"
  vlan = 4095`, `between 1 and 4094`},
		{"vif", `interface = "eth0.5"
  vlan = 10`, `ethernet interface name`},
		{"vif", `interface = "eth0"
  vlan = 10
  dhcp_options = {}`, `Empty dhcp_options block`},
		{"vif", `interface = "eth0"
  vlan = 10
  dhcp_options = { client_options = ["send vendor-class-identifier \"udhcp 1.25.1\";"] }`, `Double quotes in a DHCP option.*hex`},
		{"ethernet", `name = "eth0"
  dhcp_options = { global_options = [] }`, `at least 1`},
		{"ethernet", `name = "eth0"
  dhcp_options = { client_options = ["a;", "a;"] }`, `Duplicate List Value`},
		{"switch", `name = "eth0"`, `switch interface name`},
		{"switch", `name = "switch0"
  switch_ports = ["switch1"]`, `ethernet interface name`},
		{"switch", `name = "switch0"
  switch_ports = []`, `at least 1`},
		{"switch", `name = "switch0"
  mtu = 50`, `between 68 and 9000`},
		{"switch", `name = "switch0"
  dhcp_options = {}`, `Empty dhcp_options block`},
		{"switch", `name = "switch0"
  dhcp_options = { global_options = ["option x = \"y\";"] }`, `Double quotes in a DHCP option`},
	}
	for _, tc := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config: e.config(`
resource "edgeos_interface_` + tc.res + `" "x" {
  ` + tc.body + `
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)` + strings.ReplaceAll(tc.want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}

func TestAccInterfaceVif(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("interfaces ethernet eth0 vif 100", "interfaces ethernet eth0 vif 200", "interfaces ethernet eth1 vif 100"),
			// The parent ports and the vif nobody manages are untouched.
			e.checkValue("interfaces ethernet eth0", map[string]any{
				"duplex": "auto", "speed": "auto",
				"vif": map[string]any{"30": map[string]any{"description": "not managed"}},
			}),
			e.checkValue("interfaces ethernet eth1 address", []string{"192.168.1.1/24"}),
		),
		Steps: []resource.TestStep{
			{
				PreConfig: e.set("interfaces ethernet eth0 vif 30 description", configtree.Single("not managed")),
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_vif" "wan" {
  interface      = "eth0"
  vlan           = 100
  description    = "ISP WAN"
  addresses      = ["dhcp"]
  firewall_in    = edgeos_firewall_name.wan_in.name
  firewall_local = edgeos_firewall_name.wan_local.name
  dhcp_options = {
    default_route          = true
    default_route_distance = 210
    name_server            = false
  }
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_interface_vif.wan", "id", "eth0.100"),
					resource.TestCheckResourceAttr("edgeos_interface_vif.wan", "disabled", "false"),
					// The same shape a real router returns for a VLAN WAN.
					e.checkValue("interfaces ethernet eth0 vif 100", map[string]any{
						"address": []string{"dhcp"}, "description": "ISP WAN",
						"dhcp-options": map[string]any{"default-route": "update", "default-route-distance": "210", "name-server": "no-update"},
						"firewall":     map[string]any{"in": map[string]any{"name": "WAN_IN"}, "local": map[string]any{"name": "WAN_LOCAL"}},
					}),
				),
			},
			{
				// Update in place; an option Terraform does not model survives.
				PreConfig: e.set("interfaces ethernet eth0 vif 100 egress-qos", configtree.Single("0:3")),
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_vif" "wan" {
  interface   = "eth0"
  vlan        = 100
  description = "ISP WAN static"
  addresses   = ["203.0.113.2/30"]
  mtu         = 1492
  mac         = "02:00:5e:00:53:10"
  disabled    = true
  firewall_in = edgeos_firewall_name.wan_in.name
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("interfaces ethernet eth0 vif 100", map[string]any{
					"address": []string{"203.0.113.2/30"}, "description": "ISP WAN static", "mtu": "1492",
					"mac": "02:00:5e:00:53:10", "disable": nil, "egress-qos": "0:3",
					"firewall": map[string]any{"in": map[string]any{"name": "WAN_IN"}},
				}),
			},
			{
				ResourceName:      "edgeos_interface_vif.wan",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Drift: a local firewall and a second address are added by hand.
				PreConfig: func() {
					e.set("interfaces ethernet eth0 vif 100 firewall local name", configtree.Single("WAN_LOCAL"))()
					e.set("interfaces ethernet eth0 vif 100 address", configtree.Multi("203.0.113.2/30", "198.51.100.7/32"))()
				},
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_vif" "wan" {
  interface   = "eth0"
  vlan        = 100
  description = "ISP WAN static"
  addresses   = ["203.0.113.2/30"]
  mtu         = 1492
  mac         = "02:00:5e:00:53:10"
  disabled    = true
  firewall_in = edgeos_firewall_name.wan_in.name
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("interfaces ethernet eth0 vif 100 firewall local"),
					e.checkValue("interfaces ethernet eth0 vif 100 address", []string{"203.0.113.2/30"}),
				),
			},
			{
				// Drift: the vif is deleted by hand and gets recreated.
				PreConfig: e.del("interfaces ethernet eth0 vif 100"),
				Config: e.config(ethFirewalls + `
resource "edgeos_interface_vif" "wan" {
  interface   = "eth0"
  vlan        = 100
  description = "ISP WAN static"
  addresses   = ["203.0.113.2/30"]
  mtu         = 1492
  mac         = "02:00:5e:00:53:10"
  disabled    = true
  firewall_in = edgeos_firewall_name.wan_in.name
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("interfaces ethernet eth0 vif 100 description", "ISP WAN static"),
			},
			{
				// A new VLAN ID replaces the vif.
				Config: e.config(`
resource "edgeos_interface_vif" "wan" {
  interface = "eth0"
  vlan      = 200
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("interfaces ethernet eth0 vif 100"),
					// A vif with no settings is a bare tag node.
					e.checkValue("interfaces ethernet eth0 vif 200", nil),
				),
			},
			{
				// So does a new parent.
				Config: e.config(`
resource "edgeos_interface_vif" "wan" {
  interface   = "eth1"
  vlan        = 100
  addresses   = ["192.168.100.1/24"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("interfaces ethernet eth0 vif 200"),
					e.checkValue("interfaces ethernet eth1 vif 100 address", []string{"192.168.100.1/24"}),
				),
			},
		},
	})
}

func TestAccInterfaceVifErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig: func() { e.srv.FailNextCommit("vlan setup failed") },
				Config: e.config(`
resource "edgeos_interface_vif" "x" {
  interface = "eth2"
  vlan      = 10
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*vlan setup failed`),
			},
			{
				Config: e.config(`
resource "edgeos_interface_vif" "x" {
  interface    = "eth2"
  vlan         = 10
  firewall_out = "MISSING"
}`),
				ExpectError: regexp.MustCompile(`Firewall name \[MISSING\] does not exist`),
			},
			{
				// An existing vif has to be imported.
				PreConfig: e.set("interfaces ethernet eth2 vif 10 description", configtree.Single("hand made")),
				Config: e.config(`
resource "edgeos_interface_vif" "x" {
  interface = "eth2"
  vlan      = 10
}`),
				ExpectError: regexp.MustCompile(`(?s)already exists.*Import it`),
			},
		},
	})
	if got, _ := e.srv.Get(cliPath("interfaces ethernet eth2 vif 10")); got == nil {
		t.Fatal("the hand made vif is gone")
	}
}

// Real lines from a router whose ISP wants a client identifier and the
// vendor class of the ISP's own box, the latter as hex for "udhcp 1.25.1".
const (
	optClientID = "send dhcp-client-identifier 01:a2:03:8e:03:ce:ff;"
	optVendor   = "send vendor-class-identifier 75:64:68:63:70:20:31:2e:32:35:2e:31;"
	optRFC3442  = "option rfc3442-classless-static-routes code 121 = array of unsigned integer 8;"
)

func TestAccInterfaceDHCPOptions(t *testing.T) {
	e := newAccEnv(t)
	vif := `
resource "edgeos_interface_vif" "wan" {
  interface = "eth1"
  vlan      = 100
  addresses = ["dhcp"]
  dhcp_options = {
    client_options = ["` + optClientID + `", "` + optVendor + `"]
    global_options = ["` + optRFC3442 + `"]
  }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("interfaces ethernet eth1 vif 100", "interfaces ethernet eth0 dhcp-options client-option"),
			// The global option nobody manages on eth0 is still there.
			e.checkValue("interfaces ethernet eth0 dhcp-options global-option", []string{"option hand-made code 224 = text;"}),
		),
		Steps: []resource.TestStep{
			{
				// eth0 manages only the client options; the global option set
				// by hand is left alone.
				PreConfig: e.set("interfaces ethernet eth0 dhcp-options global-option", configtree.Multi("option hand-made code 224 = text;")),
				Config: e.config(vif + `
resource "edgeos_interface_ethernet" "wan" {
  name      = "eth0"
  addresses = ["dhcp"]
  dhcp_options = {
    client_options = ["` + optClientID + `", "` + optVendor + `"]
  }
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_interface_ethernet.wan", "dhcp_options.client_options.#", "2"),
					resource.TestCheckResourceAttr("edgeos_interface_ethernet.wan", "dhcp_options.client_options.1", optVendor),
					resource.TestCheckNoResourceAttr("edgeos_interface_ethernet.wan", "dhcp_options.global_options"),
					e.checkValue("interfaces ethernet eth0 dhcp-options", map[string]any{
						"client-option": []string{optClientID, optVendor},
						"global-option": []string{"option hand-made code 224 = text;"},
					}),
					e.checkValue("interfaces ethernet eth1 vif 100", map[string]any{
						"address": []string{"dhcp"},
						"dhcp-options": map[string]any{
							"client-option": []string{optClientID, optVendor},
							"global-option": []string{optRFC3442},
						},
					}),
				),
			},
			{
				ResourceName:      "edgeos_interface_vif.wan",
				ImportState:       true,
				ImportStateId:     "eth1.100",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_interface_vif.wan",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "eth1.100",
			},
			{
				// The order of the lines matters, and eth0 now takes over the
				// global options too.
				Config: e.config(vif + `
resource "edgeos_interface_ethernet" "wan" {
  name      = "eth0"
  addresses = ["dhcp"]
  dhcp_options = {
    client_options = ["` + optVendor + `", "` + optClientID + `"]
    global_options = ["` + optRFC3442 + `"]
  }
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_ethernet.wan", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionNoop),
				}},
				Check: e.checkValue("interfaces ethernet eth0 dhcp-options", map[string]any{
					"client-option": []string{optVendor, optClientID},
					"global-option": []string{optRFC3442},
				}),
			},
			{
				// Drift: lines changed by hand are put back.
				PreConfig: func() {
					e.del("interfaces ethernet eth0 dhcp-options client-option")()
					e.set("interfaces ethernet eth0 dhcp-options client-option", configtree.Multi(optClientID))()
					e.del("interfaces ethernet eth1 vif 100 dhcp-options global-option")()
				},
				Config: e.config(vif + `
resource "edgeos_interface_ethernet" "wan" {
  name      = "eth0"
  addresses = ["dhcp"]
  dhcp_options = {
    client_options = ["` + optVendor + `", "` + optClientID + `"]
    global_options = ["` + optRFC3442 + `"]
  }
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_ethernet.wan", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_interface_vif.wan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces ethernet eth0 dhcp-options client-option", []string{optVendor, optClientID}),
					e.checkValue("interfaces ethernet eth1 vif 100 dhcp-options global-option", []string{optRFC3442}),
				),
			},
			{
				// Removing the lists from the config removes the lines.
				Config: e.config(`
resource "edgeos_interface_vif" "wan" {
  interface = "eth1"
  vlan      = 100
  addresses = ["dhcp"]
}

resource "edgeos_interface_ethernet" "wan" {
  name      = "eth0"
  addresses = ["dhcp"]
  dhcp_options = {
    client_options = ["` + optClientID + `"]
  }
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("interfaces ethernet eth1 vif 100 dhcp-options"),
					e.checkAbsent("interfaces ethernet eth0 dhcp-options global-option"),
					e.checkValue("interfaces ethernet eth0 dhcp-options client-option", []string{optClientID}),
				),
			},
			{
				// Put the hand-made global option back for CheckDestroy.
				PreConfig: e.set("interfaces ethernet eth0 dhcp-options global-option", configtree.Multi("option hand-made code 224 = text;")),
				Config: e.config(`
resource "edgeos_interface_ethernet" "wan" {
  name      = "eth0"
  addresses = ["dhcp"]
  dhcp_options = {
    client_options = ["` + optClientID + `"]
  }
}`),
				Check: e.checkValue("interfaces ethernet eth0 dhcp-options global-option", []string{"option hand-made code 224 = text;"}),
			},
		},
	})
}

const switchFirewalls = `
resource "edgeos_firewall_name" "lan_in" {
  name = "LAN_IN"
}
resource "edgeos_firewall_name" "lan_local" {
  name = "LAN_LOCAL"
}
`

func TestAccInterfaceSwitch(t *testing.T) {
	e := newAccEnv(t)
	updated := switchFirewalls + `
resource "edgeos_interface_switch" "lan" {
  name           = "switch0"
  description    = "LAN switch"
  addresses      = ["dhcp"]
  switch_ports   = ["eth3", "eth4"]
  vlan_aware     = false
  mtu            = 1500
  mac            = "02:00:5e:00:53:20"
  disabled       = true
  firewall_local = edgeos_firewall_name.lan_local.name
  dhcp_options = {
    name_server    = false
    client_options = ["` + optClientID + `"]
  }
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			// The switch survives with the option nobody manages.
			e.checkValue("interfaces switch switch0", map[string]any{"ip": map[string]any{"enable-proxy-arp": nil}}),
		),
		Steps: []resource.TestStep{
			{
				// Take over an existing switch0, the way an EdgeRouter X ships.
				PreConfig: func() {
					e.set("interfaces switch switch0 switch-port interface eth2", configtree.Flag())()
					e.set("interfaces switch switch0 switch-port interface eth3 vlan pvid", configtree.Single("10"))()
					e.set("interfaces switch switch0 ip enable-proxy-arp", configtree.Flag())()
				},
				Config: e.config(switchFirewalls + `
resource "edgeos_interface_switch" "lan" {
  name         = "switch0"
  description  = "LAN switch"
  addresses    = ["192.168.2.1/24"]
  switch_ports = ["eth2", "eth3"]
  vlan_aware   = true
  firewall_in  = edgeos_firewall_name.lan_in.name
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_interface_switch.lan", "id", "switch0"),
					resource.TestCheckNoResourceAttr("edgeos_interface_switch.lan", "disabled"),
					resource.TestCheckNoResourceAttr("edgeos_interface_switch.lan", "mtu"),
					e.checkValue("interfaces switch switch0", map[string]any{
						"address": []string{"192.168.2.1/24"}, "description": "LAN switch",
						"firewall": map[string]any{"in": map[string]any{"name": "LAN_IN"}},
						"ip":       map[string]any{"enable-proxy-arp": nil},
						"switch-port": map[string]any{
							"interface":  map[string]any{"eth2": nil, "eth3": map[string]any{"vlan": map[string]any{"pvid": "10"}}},
							"vlan-aware": "enable",
						},
					}),
				),
			},
			{
				// Update in place: eth2 leaves the switch, eth3 keeps its VLAN
				// settings, DHCP with a client identifier.
				Config: e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_switch.lan", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("interfaces switch switch0", map[string]any{
					"address": []string{"dhcp"}, "description": "LAN switch", "mtu": "1500",
					"mac": "02:00:5e:00:53:20", "disable": nil,
					"firewall":     map[string]any{"local": map[string]any{"name": "LAN_LOCAL"}},
					"dhcp-options": map[string]any{"name-server": "no-update", "client-option": []string{optClientID}},
					"ip":           map[string]any{"enable-proxy-arp": nil},
					"switch-port": map[string]any{
						"interface":  map[string]any{"eth3": map[string]any{"vlan": map[string]any{"pvid": "10"}}, "eth4": nil},
						"vlan-aware": "disable",
					},
				}),
			},
			{
				ResourceName:      "edgeos_interface_switch.lan",
				ImportState:       true,
				ImportStateId:     "switch0",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_interface_switch.lan",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "switch0",
			},
			{
				// Drift: a port is added and one removed by hand, and the
				// description is edited.
				PreConfig: func() {
					e.set("interfaces switch switch0 switch-port interface eth2", configtree.Flag())()
					e.del("interfaces switch switch0 switch-port interface eth4")()
					e.set("interfaces switch switch0 description", configtree.Single("hand edit"))()
				},
				Config: e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_switch.lan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces switch switch0 description", "LAN switch"),
					e.checkValue("interfaces switch switch0 switch-port interface", map[string]any{
						"eth3": map[string]any{"vlan": map[string]any{"pvid": "10"}}, "eth4": nil,
					}),
				),
			},
			{
				// disabled = false switches it on; removed settings go away and
				// unmanaged ones stay.
				Config: e.config(switchFirewalls + `
resource "edgeos_interface_switch" "lan" {
  name         = "switch0"
  addresses    = ["192.168.2.1/24"]
  switch_ports = ["eth3"]
  disabled     = false
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces switch switch0", map[string]any{
						"address": []string{"192.168.2.1/24"},
						"ip":      map[string]any{"enable-proxy-arp": nil},
						"switch-port": map[string]any{
							"interface": map[string]any{"eth3": map[string]any{"vlan": map[string]any{"pvid": "10"}}},
						},
					}),
					resource.TestCheckResourceAttr("edgeos_interface_switch.lan", "disabled", "false"),
				),
			},
		},
	})
}

func TestAccInterfaceSwitchCreate(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// Destroy keeps the switch and only removes what it managed.
		CheckDestroy: e.checkAbsent("interfaces switch switch2 switch-port"),
		Steps: []resource.TestStep{
			{
				// A switch that does not exist yet is created.
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name         = "switch1"
  switch_ports = ["eth2"]
}`),
				Check: e.checkValue("interfaces switch switch1", map[string]any{
					"switch-port": map[string]any{"interface": map[string]any{"eth2": nil}},
				}),
			},
			{
				// Renaming manages another switch; the first one loses its ports.
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name         = "switch2"
  switch_ports = ["eth2"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_switch.x", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("interfaces switch switch1 switch-port"),
					e.checkValue("interfaces switch switch2 switch-port interface eth2", nil),
				),
			},
			{
				// Drift: the whole switch is deleted by hand and comes back.
				PreConfig: e.del("interfaces switch switch2"),
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name         = "switch2"
  switch_ports = ["eth2"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_interface_switch.x", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("interfaces switch switch2 switch-port interface eth2", nil),
			},
		},
	})
}

func TestAccInterfaceSwitchErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// A port the router does not have.
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name         = "switch0"
  switch_ports = ["eth9"]
}`),
				ExpectError: regexp.MustCompile(`Interface eth9 does not exist`),
			},
			{
				// A port that is already in another switch.
				PreConfig: e.set("interfaces switch switch1 switch-port interface eth2", configtree.Flag()),
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name         = "switch0"
  switch_ports = ["eth2"]
}`),
				ExpectError: regexp.MustCompile(`Interface eth2 is already a port of`),
			},
			{
				PreConfig: func() { e.srv.FailNextCommit("switch0: hardware error") },
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name        = "switch0"
  description = "never"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*switch0: hardware error`),
			},
			{
				Config: e.config(`
resource "edgeos_interface_switch" "x" {
  name        = "switch0"
  firewall_in = "MISSING"
}`),
				ExpectError: regexp.MustCompile(`Firewall name \[MISSING\] does not exist`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("interfaces switch switch0")); ok {
		t.Fatal("switch0 was created by a failed commit")
	}
}
