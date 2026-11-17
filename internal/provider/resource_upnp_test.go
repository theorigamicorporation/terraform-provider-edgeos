// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func upnpRule(action, desc, subnet, ext, local string) attr.Value {
	str := func(s string) types.String {
		if s == "" {
			return types.StringNull()
		}
		return types.StringValue(s)
	}
	return types.ObjectValueMust(upnpACLRuleType.AttrTypes, map[string]attr.Value{
		"action": str(action), "description": str(desc), "subnet": str(subnet),
		"external_port": str(ext), "local_port": str(local),
	})
}

func TestUPnPMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[upnpModel](newUPnPResource())
	cases := []struct {
		name   string
		model  upnpModel
		leaves map[string]configtree.Value
	}{
		{
			name: "minimal",
			model: upnpModel{
				ListenOn: stringSet([]string{"switch0"}), WAN: types.StringValue("eth0"),
				NATPMP: types.BoolNull(), SecureMode: types.BoolNull(), ACLRules: types.MapNull(upnpACLRuleType),
			},
			leaves: map[string]configtree.Value{
				"listen-on": configtree.UnorderedMulti("switch0"),
				"wan":       configtree.Single("eth0"),
			},
		},
		{
			name: "full",
			model: upnpModel{
				ListenOn: stringSet([]string{"switch0", "eth2"}), WAN: types.StringValue("eth0.555"),
				NATPMP: types.BoolValue(true), SecureMode: types.BoolValue(false),
				ACLRules: types.MapValueMust(upnpACLRuleType, map[string]attr.Value{
					"10": upnpRule("allow", "LAN high ports", "192.168.1.0/24", "1024-65535", "1024-65535"),
					"20": upnpRule("deny", "", "", "", ""),
				}),
			},
			leaves: map[string]configtree.Value{
				"listen-on":                 configtree.UnorderedMulti("eth2", "switch0"),
				"wan":                       configtree.Single("eth0.555"),
				"nat-pmp":                   configtree.Single("enable"),
				"secure-mode":               configtree.Single("disable"),
				"acl rule 10":               configtree.Flag(),
				"acl rule 10 action":        configtree.Single("allow"),
				"acl rule 10 description":   configtree.Single("LAN high ports"),
				"acl rule 10 subnet":        configtree.Single("192.168.1.0/24"),
				"acl rule 10 external-port": configtree.Single("1024-65535"),
				"acl rule 10 local-port":    configtree.Single("1024-65535"),
				"acl rule 20":               configtree.Flag(),
				"acl rule 20 action":        configtree.Single("deny"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := spec.ToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := upnpModel{}
			spec.FromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), tc.leaves)
			if !back.ACLRules.Equal(tc.model.ACLRules) {
				t.Errorf("acl_rules = %v, want %v", back.ACLRules, tc.model.ACLRules)
			}
		})
	}
}

func TestUPnPImport(t *testing.T) {
	t.Parallel()
	spec := specOf[upnpModel](newUPnPResource())
	if _, err := spec.ImportAttrs("upnp2"); err != nil {
		t.Error(err)
	}
	for _, id := range []string{"upnp", "service upnp2", ""} {
		if _, err := spec.ImportAttrs(id); err == nil {
			t.Errorf("%q must not be a valid ID", id)
		}
	}
}

// Acceptance tests against the fake router.

const upnpConfig = `
resource "edgeos_interface_vif" "wan" {
  interface = "eth0"
  vlan      = 555
}

resource "edgeos_service_upnp" "this" {
  listen_on   = ["eth1"]
  wan         = edgeos_interface_vif.wan.id
  nat_pmp     = true
  secure_mode = true
}`

func upnpWithACL() string {
	return strings.Replace(upnpConfig, `  secure_mode = true
`, `  secure_mode = true
  acl_rules = {
    "10" = {
      action        = "allow"
      description   = "LAN high ports"
      subnet        = "192.168.1.0/24"
      external_port = "1024-65535"
      local_port    = "1024-65535"
    }
    "9999" = {
      action = "deny"
    }
  }
`, 1)
}

func TestAccServiceUPnP(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("service upnp2"),
		Steps: []resource.TestStep{
			{
				Config: e.config(upnpConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_service_upnp.this", "id", "upnp2"),
					resource.TestCheckResourceAttr("edgeos_service_upnp.this", "wan", "eth0.555"),
					e.checkValue("service upnp2", map[string]any{
						"listen-on": []string{"eth1"}, "wan": "eth0.555", "nat-pmp": "enable", "secure-mode": "enable",
					}),
					// The rest of service is untouched.
					e.checkValue("service ssh port", "22"),
				),
			},
			{
				// Rules and another LAN are an update in place; an option the
				// resource does not model is left alone.
				PreConfig: e.set("service upnp2 port", configtree.Single("5000")),
				Config:    e.config(strings.Replace(upnpWithACL(), `["eth1"]`, `["eth1", "eth2"]`, 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_upnp.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service upnp2 listen-on", []string{"eth1", "eth2"}),
					e.checkValue("service upnp2 acl rule", map[string]any{
						"10": map[string]any{
							"action": "allow", "description": "LAN high ports", "subnet": "192.168.1.0/24",
							"external-port": "1024-65535", "local-port": "1024-65535",
						},
						"9999": map[string]any{"action": "deny"},
					}),
					e.checkValue("service upnp2 port", "5000"),
				),
			},
			{
				ResourceName:      "edgeos_service_upnp.this",
				ImportState:       true,
				ImportStateId:     "upnp2",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_service_upnp.this",
				Config:          e.importBlockConfig(strings.Replace(upnpWithACL(), `["eth1"]`, `["eth1", "eth2"]`, 1)),
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "upnp2",
			},
			{
				// Drift: a rule added and secure mode switched off by hand.
				PreConfig: func() {
					e.set("service upnp2 acl rule 50 action", configtree.Single("allow"))()
					e.set("service upnp2 secure-mode", configtree.Single("disable"))()
				},
				Config: e.config(upnpWithACL()),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_upnp.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("service upnp2 acl rule 50"),
					e.checkValue("service upnp2 secure-mode", "enable"),
					e.checkValue("service upnp2 listen-on", []string{"eth1"}),
				),
			},
			{
				// Dropping the rules and the flags removes them.
				Config: e.config(`
resource "edgeos_interface_vif" "wan" {
  interface = "eth0"
  vlan      = 555
}

resource "edgeos_service_upnp" "this" {
  listen_on = ["eth1"]
  wan       = edgeos_interface_vif.wan.id
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("service upnp2 acl"),
					e.checkAbsent("service upnp2 nat-pmp"),
					e.checkAbsent("service upnp2 secure-mode"),
				),
			},
			{
				// UPnP switched off by hand is set up again.
				PreConfig: e.del("service upnp2"),
				Config:    e.config(upnpConfig),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_upnp.this", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("service upnp2 nat-pmp", "enable"),
			},
		},
	})
}

func TestAccServiceUPnPErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// An existing upnp2 node has to be imported.
				PreConfig: func() {
					e.set("service upnp2 listen-on", configtree.Multi("eth1"))()
					e.set("service upnp2 wan", configtree.Single("eth0"))()
				},
				Config: e.config(`
resource "edgeos_service_upnp" "this" {
  listen_on = ["eth1"]
  wan       = "eth0"
}`),
				ExpectError: regexp.MustCompile(`(?s)Already exists.*import block using id "upnp2"`),
			},
			{
				PreConfig: func() {
					e.del("service upnp2")()
					e.srv.FailNextCommit("miniupnpd: cannot bind")
				},
				Config: e.config(`
resource "edgeos_service_upnp" "this" {
  listen_on = ["eth1"]
  wan       = "eth0"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*miniupnpd: cannot bind`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("service upnp2")); ok {
		t.Fatal("failed commits must leave the router unchanged")
	}
}

func TestAccServiceUPnPValidation(t *testing.T) {
	e := newAccEnv(t)
	base := "listen_on = [\"switch0\"]\n  wan = \"eth0\"\n  "
	for body, want := range map[string]string{
		`wan = "eth0"`:                                                                 `"listen_on" is required`,
		`listen_on = ["switch0"]`:                                                      `"wan" is required`,
		`listen_on = []` + "\n" + `wan = "eth0"`:                                       `at least 1`,
		`listen_on = ["eth0", "switch0"]` + "\nwan = \"eth0\"":                         `WAN interface listed as LAN`,
		`listen_on = ["LAN"]` + "\nwan = \"eth0\"":                                     `must be an interface name`,
		base + `acl_rules = { "0" = { action = "allow" } }`:                            `rule number from 1 to 9999`,
		base + `acl_rules = { "10000" = { action = "allow" } }`:                        `rule number from 1 to 9999`,
		base + `acl_rules = { "1" = { action = "permit" } }`:                           `value must be one of`,
		base + `acl_rules = { "1" = { description = "x" } }`:                           `action`,
		base + `acl_rules = { "1" = { action = "allow", subnet = "192.168.1.1/24" } }`: `host bits set`,
		base + `acl_rules = { "1" = { action = "allow", local_port = "80,443" } }`:     `must be a port or a range`,
		base + `acl_rules = {}`:                                                        `at least 1`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_service_upnp\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
