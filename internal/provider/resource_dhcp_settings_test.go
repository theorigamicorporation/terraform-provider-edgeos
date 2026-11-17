// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func nullDHCPServerSettings() dhcpServerSettingsModel {
	return dhcpServerSettingsModel{
		ID: types.StringNull(), Disabled: types.BoolNull(), HostfileUpdate: types.BoolNull(),
		StaticARP: types.BoolNull(), UseDnsmasq: types.BoolNull(), GlobalParameters: types.ListNull(types.StringType),
	}
}

func TestDHCPServerSettingsMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[dhcpServerSettingsModel](newDHCPServerSettingsResource())
	full := dhcpServerSettingsModel{
		Disabled: types.BoolValue(false), HostfileUpdate: types.BoolValue(true), StaticARP: types.BoolValue(false),
		UseDnsmasq:       types.BoolValue(true),
		GlobalParameters: stringList([]string{"option space ubnt;", "option ubnt.unifi-address code 1 = ip-address;"}),
	}
	cases := []struct {
		name   string
		model  dhcpServerSettingsModel
		leaves map[string]configtree.Value
	}{
		{"nothing managed", nullDHCPServerSettings(), map[string]configtree.Value{}},
		{"everything", full, map[string]configtree.Value{
			"disabled":          configtree.Single("false"),
			"hostfile-update":   configtree.Single("enable"),
			"static-arp":        configtree.Single("disable"),
			"use-dnsmasq":       configtree.Single("enable"),
			"global-parameters": configtree.Multi("option space ubnt;", "option ubnt.unifi-address code 1 = ip-address;"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := spec.ToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := nullDHCPServerSettings() // as after an import
			spec.FromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}

	// Only managed attributes are refreshed, and shared networks are never owned.
	device := map[string]any{
		"disabled": "true", "use-dnsmasq": "disable",
		"shared-network-name": map[string]any{"LAN": map[string]any{"authoritative": "enable"}},
	}
	partial := nullDHCPServerSettings()
	partial.Disabled = types.BoolValue(false)
	var diags diag.Diagnostics
	spec.FromTree(ctx, device, &partial, &diags)
	if !partial.Disabled.ValueBool() || !partial.UseDnsmasq.IsNull() {
		t.Fatalf("partial refresh = %+v", partial)
	}
	imported := nullDHCPServerSettings()
	spec.FromTree(ctx, device, &imported, &diags)
	if imported.UseDnsmasq.ValueBool() || imported.UseDnsmasq.IsNull() || !imported.StaticARP.IsNull() {
		t.Fatalf("import refresh = %+v", imported)
	}
	for _, p := range spec.ToLeaves(ctx, &imported, &diags).Paths() {
		if p[0] == "shared-network-name" {
			t.Fatalf("settings own %v", p)
		}
	}
}

func TestDHCPServerSettingsImport(t *testing.T) {
	t.Parallel()
	spec := specOf[dhcpServerSettingsModel](newDHCPServerSettingsResource())
	if _, err := spec.ImportAttrs("dhcp-server"); err != nil {
		t.Error(err)
	}
	if _, err := spec.ImportAttrs("LAN"); err == nil {
		t.Error("only dhcp-server is a valid ID")
	}
}

// Acceptance tests against the fake router.

const dhcpServerSettingsConfig = `
resource "edgeos_dhcp_server_settings" "this" {
  disabled          = false
  hostfile_update   = true
  static_arp        = false
  use_dnsmasq       = false
  global_parameters = ["option space ubnt;", "option ubnt.unifi-address code 1 = ip-address;"]
}`

const dhcpServerSettingsUpdated = `
resource "edgeos_dhcp_server_settings" "this" {
  disabled          = false
  hostfile_update   = false
  static_arp        = true
  use_dnsmasq       = true
  global_parameters = ["option ubnt.unifi-address code 1 = ip-address;", "option space ubnt;"]
}`

func TestAccDHCPServerSettings(t *testing.T) {
	e := newAccEnv(t)
	lan := map[string]any{"subnet": map[string]any{"192.168.1.0/24": map[string]any{"default-router": "192.168.1.1"}}}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("service dhcp-server disabled", "service dhcp-server hostfile-update",
				"service dhcp-server static-arp", "service dhcp-server use-dnsmasq", "service dhcp-server global-parameters"),
			// The shared network made by hand stays.
			e.checkValue("service dhcp-server shared-network-name LAN", lan),
		),
		Steps: []resource.TestStep{
			{
				PreConfig: e.set("service dhcp-server shared-network-name LAN subnet 192.168.1.0/24 default-router",
					configtree.Single("192.168.1.1")),
				Config: e.config(dhcpServerSettingsConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_dhcp_server_settings.this", "id", "dhcp-server"),
					e.checkValue("service dhcp-server", map[string]any{
						"disabled": "false", "hostfile-update": "enable", "static-arp": "disable", "use-dnsmasq": "disable",
						"global-parameters":   []string{"option space ubnt;", "option ubnt.unifi-address code 1 = ip-address;"},
						"shared-network-name": map[string]any{"LAN": lan},
					}),
				),
			},
			{
				// Reordering the parameters is an update.
				Config: e.config(dhcpServerSettingsUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_server_settings.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service dhcp-server hostfile-update", "disable"),
					e.checkValue("service dhcp-server static-arp", "enable"),
					e.checkValue("service dhcp-server use-dnsmasq", "enable"),
					e.checkValue("service dhcp-server global-parameters",
						[]string{"option ubnt.unifi-address code 1 = ip-address;", "option space ubnt;"}),
					e.checkValue("service dhcp-server shared-network-name LAN", lan),
				),
			},
			{
				ResourceName:      "edgeos_dhcp_server_settings.this",
				ImportState:       true,
				ImportStateId:     "dhcp-server",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_dhcp_server_settings.this",
				Config:          e.importBlockConfig(dhcpServerSettingsUpdated),
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "dhcp-server",
			},
			{
				// Drift: the server switched off and a parameter dropped by hand.
				PreConfig: func() {
					e.set("service dhcp-server disabled", configtree.Single("true"))()
					e.set("service dhcp-server global-parameters", configtree.Multi("option space ubnt;"))()
				},
				Config: e.config(dhcpServerSettingsUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_server_settings.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service dhcp-server disabled", "false"),
					e.checkValue("service dhcp-server global-parameters",
						[]string{"option ubnt.unifi-address code 1 = ip-address;", "option space ubnt;"}),
				),
			},
			{
				// Dropped attributes are deleted, even when changed by hand, and no longer read.
				PreConfig: e.set("service dhcp-server hostfile-update", configtree.Single("enable")),
				Config: e.config(`
resource "edgeos_dhcp_server_settings" "this" {
  disabled = false
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("edgeos_dhcp_server_settings.this", "hostfile_update"),
					e.checkAbsent("service dhcp-server use-dnsmasq"),
					e.checkAbsent("service dhcp-server hostfile-update"),
					e.checkValue("service dhcp-server disabled", "false"),
				),
			},
		},
	})
}

func TestAccDHCPServerSettingsCreatesNode(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// Without shared networks the node is gone after destroy.
		CheckDestroy: e.destroyed("service dhcp-server"),
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_dhcp_server_settings" "this" {
  use_dnsmasq = true
}`),
				Check: e.checkValue("service dhcp-server", map[string]any{"use-dnsmasq": "enable"}),
			},
		},
	})
}

func TestAccDHCPServerSettingsErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("dhcpd: configuration file errors") },
				Config:      e.config(dhcpServerSettingsConfig),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*configuration file errors`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("service dhcp-server")); ok {
		t.Fatal("failed commits must leave the router unchanged")
	}
}

func TestAccDHCPServerSettingsValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		``:                                       `Nothing to manage`,
		`global_parameters = []`:                 `at least 1`,
		`global_parameters = ["say \"hi\";"]`:    `double quote`,
		`global_parameters = [""]`:               `length`,
		`use_dnsmasq = "yes"`:                    `bool`,
		`disabled = false` + "\n" + `subnet = 1`: `Unsupported argument`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_dhcp_server_settings\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
