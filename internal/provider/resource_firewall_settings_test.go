// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func nullFirewallSettings() firewallSettingsModel {
	return firewallSettingsModel{
		ID: types.StringNull(), AllPing: types.BoolNull(), BroadcastPing: types.BoolNull(), IPSrcRoute: types.BoolNull(),
		IPv6SrcRoute: types.BoolNull(), IPv6ReceiveRedirects: types.BoolNull(), LogMartians: types.BoolNull(),
		ReceiveRedirects: types.BoolNull(), SendRedirects: types.BoolNull(), SynCookies: types.BoolNull(),
		SourceValidation: types.StringNull(),
	}
}

func TestFirewallSettingsMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[firewallSettingsModel](newFirewallSettingsResource())
	full := firewallSettingsModel{
		AllPing: types.BoolValue(false), BroadcastPing: types.BoolValue(true), IPSrcRoute: types.BoolValue(true),
		IPv6SrcRoute: types.BoolValue(true), IPv6ReceiveRedirects: types.BoolValue(true), LogMartians: types.BoolValue(false),
		ReceiveRedirects: types.BoolValue(true), SendRedirects: types.BoolValue(false), SynCookies: types.BoolValue(false),
		SourceValidation: types.StringValue("strict"),
	}
	cases := []struct {
		name   string
		model  firewallSettingsModel
		leaves map[string]configtree.Value
	}{
		{"nothing managed", nullFirewallSettings(), map[string]configtree.Value{}},
		{"everything", full, map[string]configtree.Value{
			"all-ping":               configtree.Single("disable"),
			"broadcast-ping":         configtree.Single("enable"),
			"ip-src-route":           configtree.Single("enable"),
			"ipv6-src-route":         configtree.Single("enable"),
			"ipv6-receive-redirects": configtree.Single("enable"),
			"log-martians":           configtree.Single("disable"),
			"receive-redirects":      configtree.Single("enable"),
			"send-redirects":         configtree.Single("disable"),
			"syn-cookies":            configtree.Single("disable"),
			"source-validation":      configtree.Single("strict"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := spec.ToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := nullFirewallSettings() // as after an import
			spec.FromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}

	// The defaults written on destroy are the factory values.
	assertLeaves(t, spec.Reset(&full), map[string]configtree.Value{
		"all-ping":               configtree.Single("enable"),
		"broadcast-ping":         configtree.Single("disable"),
		"ip-src-route":           configtree.Single("disable"),
		"ipv6-src-route":         configtree.Single("disable"),
		"ipv6-receive-redirects": configtree.Single("disable"),
		"log-martians":           configtree.Single("enable"),
		"receive-redirects":      configtree.Single("disable"),
		"send-redirects":         configtree.Single("enable"),
		"syn-cookies":            configtree.Single("enable"),
		"source-validation":      configtree.Single("disable"),
	})
}

func TestFirewallSettingsFromTreeReadsOnlyManagedAttributes(t *testing.T) {
	t.Parallel()
	spec := specOf[firewallSettingsModel](newFirewallSettingsResource())
	device := map[string]any{
		"all-ping": "disable", "syn-cookies": "enable", "source-validation": "loose",
		"name": map[string]any{"WAN_IN": map[string]any{"default-action": "drop"}},
	}
	m := nullFirewallSettings()
	m.AllPing = types.BoolValue(true)
	spec.FromTree(context.Background(), device, &m, nil)
	if m.AllPing.ValueBool() || !m.SynCookies.IsNull() || !m.SourceValidation.IsNull() {
		t.Errorf("partial refresh = %+v", m)
	}
	// The rule sets are never owned.
	var diags diag.Diagnostics
	imported := nullFirewallSettings()
	spec.FromTree(context.Background(), device, &imported, &diags)
	if imported.SourceValidation.ValueString() != "loose" || !imported.SynCookies.ValueBool() || !imported.LogMartians.IsNull() {
		t.Errorf("import refresh = %+v", imported)
	}
	for _, p := range spec.ToLeaves(context.Background(), &imported, &diags).Paths() {
		if p[0] == "name" {
			t.Fatalf("settings own %v", p)
		}
	}
}

func TestFirewallSettingsImport(t *testing.T) {
	t.Parallel()
	spec := specOf[firewallSettingsModel](newFirewallSettingsResource())
	attrs, err := spec.ImportAttrs("firewall")
	if err != nil || attrs["id"].(types.String).ValueString() != "firewall" {
		t.Errorf("import firewall: %v %v", attrs, err)
	}
	if _, err := spec.ImportAttrs("WAN_IN"); err == nil {
		t.Error("only firewall is a valid ID")
	}
}

// Acceptance tests against the fake router.

// importBlockConfig is config for an ImportBlockWithID step. Terraform plans
// an import block from a fresh working directory, so unlike the other steps
// the provider source has to be spelled out.
func (e *accEnv) importBlockConfig(body string) string {
	host := os.Getenv("TF_ACC_PROVIDER_HOST")
	if host == "" {
		host = "registry.terraform.io"
	}
	return fmt.Sprintf(`
terraform {
  required_providers {
    edgeos = {
      source = "%s/theorigamicorporation/edgeos"
    }
  }
}
`, host) + e.config(body)
}

// firewallSettingsConfig holds the factory values, which most routers keep.
const firewallSettingsConfig = `
resource "edgeos_firewall_settings" "this" {
  all_ping               = true
  broadcast_ping         = false
  ip_src_route           = false
  ipv6_receive_redirects = false
  ipv6_src_route         = false
  log_martians           = true
  receive_redirects      = false
  send_redirects         = true
  source_validation      = "disable"
  syn_cookies            = true
}`

const firewallSettingsHardened = `
resource "edgeos_firewall_settings" "this" {
  all_ping               = false
  broadcast_ping         = false
  ip_src_route           = false
  ipv6_receive_redirects = false
  ipv6_src_route         = false
  log_martians           = true
  receive_redirects      = false
  send_redirects         = false
  source_validation      = "strict"
  syn_cookies            = true
}`

func TestAccFirewallSettings(t *testing.T) {
	e := newAccEnv(t)
	ruleSet := map[string]any{"default-action": "drop", "rule": map[string]any{"10": map[string]any{"action": "accept"}}}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			// Destroy writes the factory values back and leaves the rule set alone.
			e.checkValue("firewall", map[string]any{
				"all-ping": "enable", "broadcast-ping": "disable", "ipv6-receive-redirects": "disable",
				"ipv6-src-route": "disable", "ip-src-route": "disable", "log-martians": "enable",
				"receive-redirects": "disable", "send-redirects": "enable", "source-validation": "disable",
				"syn-cookies": "enable", "name": map[string]any{"WAN_IN": ruleSet},
			}),
		),
		Steps: []resource.TestStep{
			{
				// Adopting the factory values changes nothing on the router.
				PreConfig: func() {
					e.set("firewall name WAN_IN default-action", configtree.Single("drop"))()
					e.set("firewall name WAN_IN rule 10 action", configtree.Single("accept"))()
				},
				Config: e.config(firewallSettingsConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_firewall_settings.this", "id", "firewall"),
					resource.TestCheckResourceAttr("edgeos_firewall_settings.this", "all_ping", "true"),
					e.checkValue("firewall all-ping", "enable"),
					e.checkValue("firewall name WAN_IN", ruleSet),
				),
			},
			{
				Config: e.config(firewallSettingsHardened),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_settings.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("firewall all-ping", "disable"),
					e.checkValue("firewall send-redirects", "disable"),
					e.checkValue("firewall source-validation", "strict"),
					e.checkValue("firewall name WAN_IN", ruleSet),
				),
			},
			{
				ResourceName:      "edgeos_firewall_settings.this",
				ImportState:       true,
				ImportStateId:     "firewall",
				ImportStateVerify: true,
			},
			{
				// The same import as an import block in the configuration.
				Config:          e.importBlockConfig(firewallSettingsHardened),
				ResourceName:    "edgeos_firewall_settings.this",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "firewall",
			},
			{
				// Drift: one value changed and one deleted by hand.
				PreConfig: func() {
					e.set("firewall source-validation", configtree.Single("loose"))()
					e.del("firewall all-ping")()
				},
				Config: e.config(firewallSettingsHardened),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_settings.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("firewall source-validation", "strict"),
					e.checkValue("firewall all-ping", "disable"),
				),
			},
			{
				// Dropping an attribute deletes its leaf and stops managing it.
				Config: e.config(`
resource "edgeos_firewall_settings" "this" {
  all_ping          = false
  source_validation = "strict"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("firewall send-redirects"),
					e.checkAbsent("firewall syn-cookies"),
					e.checkValue("firewall source-validation", "strict"),
					resource.TestCheckNoResourceAttr("edgeos_firewall_settings.this", "syn_cookies"),
				),
			},
			{
				// Back to every option before destroy.
				Config: e.config(firewallSettingsHardened),
				Check:  e.checkValue("firewall send-redirects", "disable"),
			},
		},
	})
}

func TestAccFirewallSettingsErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("sysctl: cannot set net.ipv4.tcp_syncookies") },
				Config:      e.config(firewallSettingsHardened),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*tcp_syncookies`),
			},
		},
	})
	if got, _ := e.srv.Get(cliPath("firewall all-ping")); got != "enable" {
		t.Fatalf("all-ping changed after a failed commit: %v", got)
	}
}

func TestAccFirewallSettingsValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		``:                               `Nothing to manage`,
		`source_validation = "medium"`:   `value must be one of`,
		`all_ping = "sometimes"`:         `bool`,
		`syn_cookies = true` + "\nx = 1": `Unsupported argument`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_firewall_settings\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
