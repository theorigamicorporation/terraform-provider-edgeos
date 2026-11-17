// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
)

// Unit tests for the model mapping.

func TestFirewallRuleMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		model  firewallRuleModel
		leaves map[string]configtree.Value
	}{
		{
			name:   "minimal",
			model:  firewallRuleModel{Action: types.StringValue("accept"), Disabled: types.BoolValue(false)},
			leaves: map[string]configtree.Value{"action": configtree.Single("accept")},
		},
		{
			name: "established related",
			model: firewallRuleModel{
				Action:      types.StringValue("accept"),
				Description: types.StringValue("allow established"),
				Disabled:    types.BoolValue(true),
				Log:         types.BoolValue(false),
				State: &fwStateModel{
					Established: types.BoolValue(true), Related: types.BoolValue(true),
					New: types.BoolNull(), Invalid: types.BoolValue(false),
				},
			},
			leaves: map[string]configtree.Value{
				"action":            configtree.Single("accept"),
				"description":       configtree.Single("allow established"),
				"disable":           configtree.Flag(),
				"log":               configtree.Single("disable"),
				"state established": configtree.Single("enable"),
				"state related":     configtree.Single("enable"),
				"state invalid":     configtree.Single("disable"),
			},
		},
		{
			name: "ports groups icmp flags",
			model: firewallRuleModel{
				Action:   types.StringValue("drop"),
				Protocol: types.StringValue("tcp"),
				Disabled: types.BoolValue(false),
				Source: &fwSource{
					Address: types.StringValue("!192.0.2.0/24"), Port: types.StringValue("1024-65535"),
					MACAddress: types.StringValue("02:00:5e:00:53:01"), AddressGroup: types.StringNull(),
					NetworkGroup: types.StringValue("BOGONS"), PortGroup: types.StringNull(),
				},
				Destination: &fwDest{
					Address: types.StringValue("198.51.100.10"), Port: types.StringValue("http,https"),
					AddressGroup: types.StringNull(), NetworkGroup: types.StringNull(), PortGroup: types.StringValue("WEB"),
				},
				ICMP:     &fwICMP{TypeName: types.StringNull(), Type: types.Int64Value(3), Code: types.Int64Value(4)},
				TCPFlags: types.StringValue("SYN,!ACK"),
			},
			leaves: map[string]configtree.Value{
				"action":                       configtree.Single("drop"),
				"protocol":                     configtree.Single("tcp"),
				"source address":               configtree.Single("!192.0.2.0/24"),
				"source port":                  configtree.Single("1024-65535"),
				"source mac-address":           configtree.Single("02:00:5e:00:53:01"),
				"source group network-group":   configtree.Single("BOGONS"),
				"destination address":          configtree.Single("198.51.100.10"),
				"destination port":             configtree.Single("http,https"),
				"destination group port-group": configtree.Single("WEB"),
				"icmp type":                    configtree.Single("3"),
				"icmp code":                    configtree.Single("4"),
				"tcp flags":                    configtree.Single("SYN,!ACK"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := firewallRuleToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)

			// Round trip through the tree the router would store.
			back := firewallRuleModel{}
			firewallRuleFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, firewallRuleToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}
}

func assertLeaves(t *testing.T, l configtree.Leaves, want map[string]configtree.Value) {
	t.Helper()
	if l.Len() != len(want) {
		t.Errorf("got %d leaves, want %d: %v", l.Len(), len(want), l.Paths())
	}
	for ps, v := range want {
		got, ok := l.Get(cliPath(ps))
		if !ok || !got.Equal(v) {
			t.Errorf("leaf %q = %#v (present %v), want %#v", ps, got, ok, v)
		}
	}
}

func TestFirewallRuleFromTreeIgnoresEmptyContainers(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	tree := map[string]any{"action": "accept", "source": map[string]any{}, "state": map[string]any{}, "icmp": map[string]any{"type": "x"}}
	m := firewallRuleModel{}
	firewallRuleFromTree(context.Background(), tree, &m, &diags)
	if m.Source != nil || m.State != nil {
		t.Fatalf("empty containers became objects: %+v %+v", m.Source, m.State)
	}
	if !diags.HasError() {
		t.Fatal("a non-numeric ICMP type should be reported")
	}
}

func TestRulesetNumberImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{
		"WAN_IN:10": true, "a.b-c:9999": true, "WAN_IN": false, "WAN_IN:0": false,
		"WAN_IN:10000": false, "bad name:1": false, "WAN_IN:x": false,
	} {
		_, err := rulesetNumberImport(id)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

// Acceptance tests against the fake router.

func TestAccFirewallName(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("firewall name LAN_IN", "firewall name LAN_LOCAL"),
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_firewall_name" "lan" {
  name        = "LAN_IN"
  description = "LAN to anywhere"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_firewall_name.lan", "id", "LAN_IN"),
					resource.TestCheckResourceAttr("edgeos_firewall_name.lan", "default_action", "drop"),
					resource.TestCheckResourceAttr("edgeos_firewall_name.lan", "enable_default_log", "false"),
					e.checkValue("firewall name LAN_IN", map[string]any{"default-action": "drop", "description": "LAN to anywhere"}),
				),
			},
			{
				// Update in place.
				Config: e.config(`
resource "edgeos_firewall_name" "lan" {
  name               = "LAN_IN"
  default_action     = "accept"
  enable_default_log = true
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_name.lan", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("firewall name LAN_IN", map[string]any{"default-action": "accept", "enable-default-log": nil}),
			},
			{
				ResourceName:      "edgeos_firewall_name.lan",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Drift: someone changes the default action and adds a description.
				PreConfig: func() {
					e.set("firewall name LAN_IN default-action", configtree.Single("reject"))()
					e.set("firewall name LAN_IN description", configtree.Single("hand edit"))()
				},
				Config: e.config(`
resource "edgeos_firewall_name" "lan" {
  name               = "LAN_IN"
  default_action     = "accept"
  enable_default_log = true
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_name.lan", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("firewall name LAN_IN", map[string]any{"default-action": "accept", "enable-default-log": nil}),
			},
			{
				// Drift: the rule set is deleted out of band and gets recreated.
				PreConfig: e.del("firewall name LAN_IN"),
				Config: e.config(`
resource "edgeos_firewall_name" "lan" {
  name               = "LAN_IN"
  default_action     = "accept"
  enable_default_log = true
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_name.lan", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("firewall name LAN_IN default-action", "accept"),
			},
			{
				// Renaming replaces the rule set.
				Config: e.config(`
resource "edgeos_firewall_name" "lan" {
  name = "LAN_LOCAL"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_name.lan", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("firewall name LAN_IN"),
					e.checkValue("firewall name LAN_LOCAL default-action", "drop"),
				),
			},
		},
	})
}

func TestAccFirewallRule(t *testing.T) {
	e := newAccEnv(t)
	const base = `
resource "edgeos_firewall_name" "wan_in" {
  name        = "WAN_IN"
  description = "WAN to LAN"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("firewall name WAN_IN"),
		Steps: []resource.TestStep{
			{
				Config: e.config(base + `
resource "edgeos_firewall_rule" "established" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 10
  action      = "accept"
  description = "established and related"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "ssh" {
  ruleset  = edgeos_firewall_name.wan_in.name
  number   = 20
  action   = "accept"
  protocol = "tcp"
  log      = true
  source = {
    address = "198.51.100.0/24"
  }
  destination = {
    address = "192.168.1.10"
    port    = "22"
  }
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_firewall_rule.established", "id", "WAN_IN:10"),
					resource.TestCheckResourceAttr("edgeos_firewall_rule.established", "disabled", "false"),
					resource.TestCheckNoResourceAttr("edgeos_firewall_rule.established", "source"),
					e.checkValue("firewall name WAN_IN rule 10", map[string]any{
						"action": "accept", "description": "established and related",
						"state": map[string]any{"established": "enable", "related": "enable"},
					}),
					e.checkValue("firewall name WAN_IN rule 20", map[string]any{
						"action": "accept", "protocol": "tcp", "log": "enable",
						"source":      map[string]any{"address": "198.51.100.0/24"},
						"destination": map[string]any{"address": "192.168.1.10", "port": "22"},
					}),
				),
			},
			{
				// Update in place: drop the log flag, change the port, disable the rule.
				PreConfig: e.set("firewall name WAN_IN rule 99 action", configtree.Single("drop")),
				Config: e.config(base + `
resource "edgeos_firewall_rule" "established" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 10
  action      = "accept"
  description = "established and related"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "ssh" {
  ruleset  = edgeos_firewall_name.wan_in.name
  number   = 20
  action   = "accept"
  protocol = "tcp"
  disabled = true
  destination = {
    address = "192.168.1.10"
    port    = "2222"
  }
}
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_rule.ssh", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_firewall_rule.established", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("firewall name WAN_IN rule 20", map[string]any{
						"action": "accept", "protocol": "tcp", "disable": nil,
						"destination": map[string]any{"address": "192.168.1.10", "port": "2222"},
					}),
					// A rule Terraform does not manage survives.
					e.checkValue("firewall name WAN_IN rule 99 action", "drop"),
				),
			},
			{
				ResourceName:      "edgeos_firewall_rule.ssh",
				ImportState:       true,
				ImportStateId:     "WAN_IN:20",
				ImportStateVerify: true,
			},
			{
				// Drift inside a nested attribute is detected and fixed.
				PreConfig: e.set("firewall name WAN_IN rule 10 state new", configtree.Single("enable")),
				Config: e.config(base + `
resource "edgeos_firewall_rule" "established" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 10
  action      = "accept"
  description = "established and related"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "ssh" {
  ruleset  = edgeos_firewall_name.wan_in.name
  number   = 20
  action   = "accept"
  protocol = "tcp"
  disabled = true
  destination = {
    address = "192.168.1.10"
    port    = "2222"
  }
}
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_rule.established", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("firewall name WAN_IN rule 10 state", map[string]any{"established": "enable", "related": "enable"}),
			},
			{
				// Changing the number replaces the rule.
				Config: e.config(base + `
resource "edgeos_firewall_rule" "established" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 15
  action      = "accept"
  description = "established and related"
  state = {
    established = true
    related     = true
  }
}
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_rule.established", plancheck.ResourceActionDestroyBeforeCreate),
					plancheck.ExpectResourceAction("edgeos_firewall_rule.ssh", plancheck.ResourceActionDestroy),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("firewall name WAN_IN rule 10"),
					e.checkAbsent("firewall name WAN_IN rule 20"),
					e.checkValue("firewall name WAN_IN rule 15 action", "accept"),
				),
			},
		},
	})
}

func TestAccFirewallRuleValidation(t *testing.T) {
	e := newAccEnv(t)
	cases := []struct {
		body string
		want string
	}{
		{`action = "reject-tcp"
  protocol = "udp"`, `reject-tcp needs protocol tcp`},
		{`action = "accept"
  destination = { port = "22" }`, `Port needs tcp or udp`},
		{`action = "accept"
  protocol = "tcp"
  icmp = { type = 8 }`, `ICMP match needs protocol icmp`},
		{`action = "accept"
  protocol = "icmp"
  icmp = {
    type      = 8
    type_name = "echo-request"
  }`, `Conflicting ICMP match`},
		{`action = "accept"
  state = {}`, `Empty state block`},
		{`action = "explode"`, `value must be one of`},
		{`action = "accept"
  description = "say \"hi\""`, `double quote`},
		{`action = "accept"
  source = { mac_address = "02:00:5E:00:53:01" }`, `lower case MAC`},
		{`action = "accept"
  protocol = "udp"
  tcp_flags = "SYN"`, `TCP flags need protocol tcp`},
	}
	for _, tc := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config: e.config(`
resource "edgeos_firewall_rule" "r" {
  ruleset = "X"
  number  = 1
  ` + tc.body + `
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}

func TestAccFirewallErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Commit failure: nothing is created and the error is shown.
				PreConfig: func() { e.srv.FailNextCommit("iptables-restore: line 2 failed") },
				Config: e.config(`
resource "edgeos_firewall_name" "x" {
  name = "FAILING"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*iptables-restore: line 2 failed`),
			},
			{
				// Creating a rule set that already exists suggests import.
				PreConfig: e.set("firewall name EXISTING default-action", configtree.Single("drop")),
				Config: e.config(`
resource "edgeos_firewall_name" "x" {
  name = "EXISTING"
}`),
				ExpectError: regexp.MustCompile(`(?s)already exists.*Import it`),
			},
			{
				// The session expires between runs; the provider logs in again.
				PreConfig: e.srv.ExpireSessions,
				Config: e.config(`
resource "edgeos_firewall_name" "x" {
  name = "AFTER_EXPIRY"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("firewall name AFTER_EXPIRY default-action", "drop"),
					func(*terraform.State) error {
						if e.srv.Stats().Logins < 2 {
							t.Errorf("expected a second login, stats %+v", e.srv.Stats())
						}
						return nil
					},
				),
			},
			{
				// A rule referencing a rule set that does not exist fails at commit.
				Config: e.config(`
resource "edgeos_firewall_name" "x" {
  name = "AFTER_EXPIRY"
}
resource "edgeos_config" "bad_ref" {
  path  = ["interfaces", "ethernet", "eth3", "firewall", "in", "name"]
  value = "NO_SUCH_RULESET"
}`),
				ExpectError: regexp.MustCompile(`Firewall name \[NO_SUCH_RULESET\] does not exist`),
			},
		},
	})
}

func TestAccAuthFailure(t *testing.T) {
	e := newAccEnv(t, fakeedgeos.Options{Password: "something-else!"})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: e.config(`
resource "edgeos_firewall_name" "x" {
  name = "NOPE"
}`),
			ExpectError: reLogin,
		}},
	})
}

// TestAccSerialisedCommits creates many rules at once against a slow fake
// router. Terraform runs them in parallel; the provider must not overlap
// commits.
func TestAccSerialisedCommits(t *testing.T) {
	e := newAccEnv(t, fakeedgeos.Options{CommitDelay: 20 * time.Millisecond})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: e.config(`
resource "edgeos_firewall_name" "x" {
  name = "MANY"
}
resource "edgeos_firewall_rule" "r" {
  count   = 12
  ruleset = edgeos_firewall_name.x.name
  number  = (count.index + 1) * 10
  action  = "accept"
}`),
			Check: func(*terraform.State) error {
				st := e.srv.Stats()
				if st.Overlaps != 0 {
					t.Errorf("%d overlapping commits", st.Overlaps)
				}
				if st.Commits < 13 {
					t.Errorf("commits = %d", st.Commits)
				}
				return nil
			},
		}},
	})
}
