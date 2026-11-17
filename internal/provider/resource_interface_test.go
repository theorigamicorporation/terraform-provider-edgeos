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
