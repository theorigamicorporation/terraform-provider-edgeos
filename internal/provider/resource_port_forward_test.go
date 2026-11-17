// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// specOf returns the leafSpec behind a resource factory.
func specOf[M any](f resourceFactory) *leafSpec[M] {
	return f().(*leafResource[M]).spec
}

func TestPortForwardMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[portForwardModel](newPortForwardResource())
	cases := []struct {
		name   string
		model  portForwardModel
		leaves map[string]configtree.Value
	}{
		{
			name: "minimal",
			model: portForwardModel{
				OriginalPort: types.StringValue("443"), Protocol: types.StringValue("tcp_udp"), Description: types.StringNull(),
				ForwardTo: &pfForwardTo{Address: types.StringValue("192.168.1.20"), Port: types.StringNull()},
			},
			leaves: map[string]configtree.Value{
				"original-port":      configtree.Single("443"),
				"protocol":           configtree.Single("tcp_udp"),
				"forward-to address": configtree.Single("192.168.1.20"),
			},
		},
		{
			name: "full",
			model: portForwardModel{
				OriginalPort: types.StringValue("8000-8010"), Protocol: types.StringValue("udp"), Description: types.StringValue("game"),
				ForwardTo: &pfForwardTo{Address: types.StringValue("192.168.1.21"), Port: types.StringValue("9000-9010")},
			},
			leaves: map[string]configtree.Value{
				"description":        configtree.Single("game"),
				"original-port":      configtree.Single("8000-8010"),
				"protocol":           configtree.Single("udp"),
				"forward-to address": configtree.Single("192.168.1.21"),
				"forward-to port":    configtree.Single("9000-9010"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := spec.ToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := portForwardModel{}
			spec.FromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}

	// An empty forward-to container reads as no block.
	back := portForwardModel{}
	spec.FromTree(ctx, map[string]any{"original-port": "22", "forward-to": map[string]any{}}, &back, nil)
	if back.ForwardTo != nil {
		t.Fatalf("forward_to = %+v", back.ForwardTo)
	}
}

func TestPortForwardImport(t *testing.T) {
	t.Parallel()
	spec := specOf[portForwardModel](newPortForwardResource())
	for id, ok := range map[string]bool{
		"1": true, "4294967295": true, "0": false, "4294967296": false, "a": false, "01": false,
	} {
		if _, err := spec.ImportAttrs(id); (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
	}
	settings := specOf[portForwardSettingsModel](newPortForwardSettingsResource())
	if _, err := settings.ImportAttrs("port-forward"); err != nil {
		t.Error(err)
	}
	if _, err := settings.ImportAttrs("settings"); err == nil {
		t.Error("only port-forward is a valid settings ID")
	}
}

func TestPortForwardSettingsMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[portForwardSettingsModel](newPortForwardSettingsResource())
	full := portForwardSettingsModel{
		AutoFirewall: types.BoolValue(true), HairpinNAT: types.BoolValue(false),
		WANInterface: types.StringValue("eth0"), LANInterfaces: stringSet([]string{"switch0", "eth2"}),
	}
	var diags diag.Diagnostics
	l := spec.ToLeaves(ctx, &full, &diags)
	assertLeaves(t, l, map[string]configtree.Value{
		"auto-firewall": configtree.Single("enable"),
		"hairpin-nat":   configtree.Single("disable"),
		"wan-interface": configtree.Single("eth0"),
		"lan-interface": configtree.UnorderedMulti("eth2", "switch0"),
	})

	device := map[string]any{
		"auto-firewall": "enable", "hairpin-nat": "enable", "wan-interface": "pppoe0",
		"lan-interface": []any{"switch0"}, "rule": map[string]any{"1": map[string]any{"original-port": "22"}},
	}

	// Only managed attributes are refreshed.
	partial := portForwardSettingsModel{
		AutoFirewall: types.BoolNull(), HairpinNAT: types.BoolValue(false),
		WANInterface: types.StringNull(), LANInterfaces: types.SetNull(types.StringType),
	}
	spec.FromTree(ctx, device, &partial, &diags)
	if !partial.HairpinNAT.ValueBool() || !partial.WANInterface.IsNull() || !partial.AutoFirewall.IsNull() || !partial.LANInterfaces.IsNull() {
		t.Fatalf("partial refresh = %+v", partial)
	}

	// Right after import nothing is managed yet, so everything is read.
	imported := portForwardSettingsModel{
		AutoFirewall: types.BoolNull(), HairpinNAT: types.BoolNull(),
		WANInterface: types.StringNull(), LANInterfaces: types.SetNull(types.StringType),
	}
	spec.FromTree(ctx, device, &imported, &diags)
	if imported.WANInterface.ValueString() != "pppoe0" || !imported.AutoFirewall.ValueBool() ||
		!imported.LANInterfaces.Equal(stringSet([]string{"switch0"})) {
		t.Fatalf("import refresh = %+v", imported)
	}

	// The settings never own a rule.
	for _, p := range spec.ToLeaves(ctx, &imported, &diags).Paths() {
		if p[0] == "rule" {
			t.Fatalf("settings own %v", p)
		}
	}
	if diags.HasError() {
		t.Fatal(diags)
	}
}

// Acceptance tests against the fake router.

const pfSettings = `
resource "edgeos_port_forward_settings" "pf" {
  wan_interface  = "eth0"
  lan_interfaces = ["eth1"]
  hairpin_nat    = true
  auto_firewall  = true
}
`

const pfRules = `
resource "edgeos_port_forward" "https" {
  number        = 1
  description   = "HTTPS"
  original_port = "443"
  protocol      = "tcp"
  forward_to = {
    address = "192.168.1.20"
  }
  depends_on = [edgeos_port_forward_settings.pf]
}

resource "edgeos_port_forward" "game" {
  number        = 2
  original_port = "8000-8010"
  forward_to = {
    address = "192.168.1.21"
    port    = "9000-9010"
  }
  depends_on = [edgeos_port_forward_settings.pf]
}
`

func pfUpdated() string {
	settings := strings.NewReplacer(`["eth1"]`, `["eth1", "eth2"]`, `hairpin_nat    = true`, `hairpin_nat    = false`).Replace(pfSettings)
	rules := strings.NewReplacer(`description   = "HTTPS"`, `description   = "HTTPS to the reverse proxy"`,
		`address = "192.168.1.20"`, `address = "192.168.1.20"
    port    = "8443"`).Replace(pfRules)
	return settings + rules
}

func TestAccPortForward(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("port-forward rule", "port-forward wan-interface", "port-forward lan-interface", "port-forward hairpin-nat"),
		Steps: []resource.TestStep{
			{
				Config: e.config(pfSettings + pfRules),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_port_forward.https", "id", "1"),
					resource.TestCheckResourceAttr("edgeos_port_forward.game", "protocol", "tcp_udp"),
					resource.TestCheckResourceAttr("edgeos_port_forward_settings.pf", "id", "port-forward"),
					e.checkValue("port-forward", map[string]any{
						"wan-interface": "eth0", "lan-interface": []string{"eth1"}, "hairpin-nat": "enable", "auto-firewall": "enable",
						"rule": map[string]any{
							"1": map[string]any{"description": "HTTPS", "original-port": "443", "protocol": "tcp",
								"forward-to": map[string]any{"address": "192.168.1.20"}},
							"2": map[string]any{"original-port": "8000-8010", "protocol": "tcp_udp",
								"forward-to": map[string]any{"address": "192.168.1.21", "port": "9000-9010"}},
						},
					}),
				),
			},
			{
				// Update in place, with a rule added by hand that the settings must leave alone.
				PreConfig: func() {
					e.set("port-forward rule 99 original-port", configtree.Single("22"))()
					e.set("port-forward rule 99 forward-to address", configtree.Single("192.168.1.5"))()
				},
				Config: e.config(pfUpdated()),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_port_forward.https", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_port_forward.game", plancheck.ResourceActionNoop),
					plancheck.ExpectResourceAction("edgeos_port_forward_settings.pf", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("port-forward rule 1", map[string]any{
						"description": "HTTPS to the reverse proxy", "original-port": "443", "protocol": "tcp",
						"forward-to": map[string]any{"address": "192.168.1.20", "port": "8443"},
					}),
					e.checkValue("port-forward lan-interface", []string{"eth1", "eth2"}),
					e.checkValue("port-forward hairpin-nat", "disable"),
					e.checkValue("port-forward rule 99", map[string]any{
						"original-port": "22", "forward-to": map[string]any{"address": "192.168.1.5"},
					}),
				),
			},
			{
				ResourceName:      "edgeos_port_forward.https",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "edgeos_port_forward_settings.pf",
				ImportState:       true,
				ImportStateId:     "port-forward",
				ImportStateVerify: true,
			},
			{
				// Drift on a rule and on the settings is detected and fixed.
				PreConfig: func() {
					e.set("port-forward rule 1 forward-to address", configtree.Single("192.168.1.66"))()
					e.set("port-forward wan-interface", configtree.Single("eth3"))()
				},
				Config: e.config(pfUpdated()),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_port_forward.https", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_port_forward_settings.pf", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("port-forward rule 1 forward-to address", "192.168.1.20"),
					e.checkValue("port-forward wan-interface", "eth0"),
				),
			},
			{
				// A rule deleted by hand is recreated.
				PreConfig: e.del("port-forward rule 2"),
				Config:    e.config(pfUpdated()),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_port_forward.game", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("port-forward rule 2 forward-to port", "9000-9010"),
			},
			{
				// Changing the number replaces the rule. The hand made rule is
				// removed first so the final destroy can drop the interfaces.
				PreConfig: e.del("port-forward rule 99"),
				Config:    e.config(strings.Replace(pfUpdated(), "number        = 2", "number        = 3", 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_port_forward.game", plancheck.ResourceActionDestroyBeforeCreate),
					plancheck.ExpectResourceAction("edgeos_port_forward.https", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("port-forward rule 2"),
					e.checkValue("port-forward rule 3 original-port", "8000-8010"),
				),
			},
		},
	})
}

func TestAccPortForwardSettingsLeavesRules(t *testing.T) {
	e := newAccEnv(t)
	handMade := func() {
		e.set("port-forward wan-interface", configtree.Single("eth0"))()
		e.set("port-forward lan-interface", configtree.Multi("eth1"))()
		e.set("port-forward rule 5 original-port", configtree.Single("22"))()
		e.set("port-forward rule 5 forward-to address", configtree.Single("192.168.1.5"))()
	}
	rule5 := map[string]any{"original-port": "22", "forward-to": map[string]any{"address": "192.168.1.5"}}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("port-forward hairpin-nat", "port-forward auto-firewall"),
			e.checkValue("port-forward rule 5", rule5),
			e.checkValue("port-forward wan-interface", "eth0"),
			e.checkValue("port-forward lan-interface", []string{"eth1"}),
		),
		Steps: []resource.TestStep{
			{
				// Create takes over the existing node and does not read what it does not manage.
				PreConfig: handMade,
				Config: e.config(`
resource "edgeos_port_forward_settings" "pf" {
  hairpin_nat = true
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("edgeos_port_forward_settings.pf", "wan_interface"),
					resource.TestCheckNoResourceAttr("edgeos_port_forward_settings.pf", "lan_interfaces"),
					e.checkValue("port-forward hairpin-nat", "enable"),
					e.checkValue("port-forward rule 5", rule5),
				),
			},
			{
				Config: e.config(`
resource "edgeos_port_forward_settings" "pf" {
  hairpin_nat   = false
  auto_firewall = false
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_port_forward_settings.pf", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("port-forward hairpin-nat", "disable"),
					e.checkValue("port-forward auto-firewall", "disable"),
					e.checkValue("port-forward rule 5", rule5),
					e.checkValue("port-forward wan-interface", "eth0"),
				),
			},
			{
				// A managed attribute deleted by hand is put back.
				PreConfig: e.del("port-forward auto-firewall"),
				Config: e.config(`
resource "edgeos_port_forward_settings" "pf" {
  hairpin_nat   = false
  auto_firewall = false
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_port_forward_settings.pf", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("port-forward auto-firewall", "disable"),
			},
		},
	})
}

func TestAccPortForwardErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// EdgeOS refuses rules without the interfaces.
				Config: e.config(`
resource "edgeos_port_forward" "x" {
  number        = 1
  original_port = "22"
  forward_to = {
    address = "192.168.1.5"
  }
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*wan-interface must be set`),
			},
			{
				PreConfig: func() { e.srv.FailNextCommit("port-forward: cannot write rules") },
				Config: e.config(`
resource "edgeos_port_forward_settings" "pf" {
  wan_interface = "eth0"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*cannot write rules`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("port-forward")); ok {
		t.Fatal("failed commits must leave the router unchanged")
	}
}

func TestAccPortForwardValidation(t *testing.T) {
	e := newAccEnv(t)
	rule := func(body string) string {
		return `
resource "edgeos_port_forward" "r" {
  number = 1
  ` + body + `
}`
	}
	cases := []struct {
		config string
		want   string
	}{
		{`resource "edgeos_port_forward_settings" "s" {}`, `Nothing to manage`},
		{`resource "edgeos_port_forward_settings" "s" {
  wan_interface  = "eth0"
  lan_interfaces = ["eth0", "eth1"]
}`, `WAN interface listed as LAN`},
		{`resource "edgeos_port_forward_settings" "s" {
  lan_interfaces = []
}`, `at least 1`},
		{`resource "edgeos_port_forward_settings" "s" {
  lan_interfaces = ["LAN"]
}`, `must be an interface name`},
		{rule(`original_port = "22"
  protocol = "icmp"
  forward_to = { address = "192.168.1.5" }`), `value must be one of`},
		{rule(`original_port = "ssh"
  forward_to = { address = "192.168.1.5" }`), `must be a port`},
		{rule(`original_port = "22"
  forward_to = { address = "192.168.1.300" }`), `not an IPv4 address`},
		{rule(`original_port = "22"
  forward_to = {
    address = "192.168.1.5"
    port    = "22,23"
  }`), `must be a port or a range`},
		{rule(`original_port = "22"
  description = "a \"quoted\" word"
  forward_to = { address = "192.168.1.5" }`), `double quote`},
		{rule(`original_port = "22"`), `forward_to`},
	}
	for _, tc := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config(tc.config),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
