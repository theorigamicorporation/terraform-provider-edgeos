// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestParseTree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantErr string
	}{
		{`{"cache-size": "150", "listen-on": ["switch0"], "options": null}`, ""},
		{`{"a": {"b": {"c": "d"}}}`, ""},
		{`"leaf"`, "must be a JSON object"},
		{`{`, "not valid JSON"},
		{`{}`, "at least one node"},
		{`{"port": 22}`, `"port" is a number`},
		{`{"on": true}`, "quote numbers and booleans"},
		{`{"list": []}`, "empty list"},
		{`{"list": ["a", 1]}`, "only contain strings"},
		{`{"d": "say \"hi\""}`, "double quote"},
		{`{"l": ["ok", "bad\"q"]}`, "double quote"},
		{`{"": "x"}`, "empty node name"},
		{`{"a\"b": "x"}`, "node name"},
	}
	for _, tc := range cases {
		_, err := parseTree(tc.in)
		if tc.wantErr == "" && err != nil {
			t.Errorf("parseTree(%s) = %v", tc.in, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("parseTree(%s) = %v, want %q", tc.in, err, tc.wantErr)
		}
	}
}

func TestAccConfigValue(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("service dns forwarding cache-size", "system name-server", "service unms"),
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_config" "cache" {
  path  = ["service", "dns", "forwarding", "cache-size"]
  value = "150"
}
resource "edgeos_config" "ns" {
  path   = ["system", "name-server"]
  values = ["192.0.2.53", "198.51.100.53"]
}
resource "edgeos_config" "unms" {
  path = ["service", "unms"]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_config.cache", "id", "service dns forwarding cache-size"),
					e.checkValue("service dns forwarding cache-size", "150"),
					e.checkValue("system name-server", []string{"192.0.2.53", "198.51.100.53"}),
					e.checkValue("service unms", nil),
				),
			},
			{
				// Update in place, including reordering a list.
				Config: e.config(`
resource "edgeos_config" "cache" {
  path  = ["service", "dns", "forwarding", "cache-size"]
  value = "300"
}
resource "edgeos_config" "ns" {
  path   = ["system", "name-server"]
  values = ["198.51.100.53", "192.0.2.53", "203.0.113.53"]
}
resource "edgeos_config" "unms" {
  path = ["service", "unms"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_config.cache", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_config.ns", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_config.unms", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service dns forwarding cache-size", "300"),
					e.checkValue("system name-server", []string{"198.51.100.53", "192.0.2.53", "203.0.113.53"}),
				),
			},
			{ResourceName: "edgeos_config.cache", ImportState: true, ImportStateId: "service dns forwarding cache-size", ImportStateVerify: true},
			{ResourceName: "edgeos_config.ns", ImportState: true, ImportStateId: "system name-server", ImportStateVerify: true},
			{ResourceName: "edgeos_config.unms", ImportState: true, ImportStateId: "service unms", ImportStateVerify: true},
			{ResourceName: "edgeos_config.unms", ImportState: true, ImportStateId: `service "unms`, ExpectError: regexp.MustCompile(`Invalid import ID`)},
			{
				// Drift on a value and a list.
				PreConfig: func() {
					e.set("service dns forwarding cache-size", configtree.Single("1"))()
					e.del("system name-server")()
					e.set("system name-server", configtree.Multi("203.0.113.53"))()
				},
				Config: e.config(`
resource "edgeos_config" "cache" {
  path  = ["service", "dns", "forwarding", "cache-size"]
  value = "300"
}
resource "edgeos_config" "ns" {
  path   = ["system", "name-server"]
  values = ["198.51.100.53", "192.0.2.53", "203.0.113.53"]
}
resource "edgeos_config" "unms" {
  path = ["service", "unms"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_config.cache", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_config.ns", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service dns forwarding cache-size", "300"),
					e.checkValue("system name-server", []string{"198.51.100.53", "192.0.2.53", "203.0.113.53"}),
				),
			},
			{
				// Changing the path replaces the resource.
				Config: e.config(`
resource "edgeos_config" "cache" {
  path  = ["service", "dns", "forwarding", "options"]
  value = "no-negcache"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_config.cache", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("service dns forwarding cache-size"),
					e.checkValue("service dns forwarding options", []string{"no-negcache"}),
				),
			},
		},
	})
}

func TestAccConfigTree(t *testing.T) {
	e := newAccEnv(t)
	cfg := func(tree string) string {
		return e.config(`
resource "edgeos_config" "sched" {
  path = ["system", "task-scheduler", "task", "backup"]
  tree = jsonencode(` + tree + `)
}`)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("system task-scheduler"),
		Steps: []resource.TestStep{
			{
				Config: cfg(`{
    interval   = "1d"
    executable = { path = "/config/scripts/backup.sh", arguments = "--quiet" }
  }`),
				Check: e.checkValue("system task-scheduler task backup", map[string]any{
					"interval":   "1d",
					"executable": map[string]any{"path": "/config/scripts/backup.sh", "arguments": "--quiet"},
				}),
			},
			{
				// Remove one leaf, change another.
				Config: cfg(`{
    interval   = "12h"
    executable = { path = "/config/scripts/backup.sh" }
  }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_config.sched", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("system task-scheduler task backup", map[string]any{
					"interval":   "12h",
					"executable": map[string]any{"path": "/config/scripts/backup.sh"},
				}),
			},
			{ResourceName: "edgeos_config.sched", ImportState: true, ImportStateId: "system task-scheduler task backup", ImportStateVerify: true},
			{
				// Out-of-band children are drift for a tree resource and are removed.
				PreConfig: e.set("system task-scheduler task backup crontab-spec", configtree.Single("0 3 * * *")),
				Config: cfg(`{
    interval   = "12h"
    executable = { path = "/config/scripts/backup.sh" }
  }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_config.sched", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkAbsent("system task-scheduler task backup crontab-spec"),
			},
		},
	})
}

func TestAccConfigErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_config" "x" {
  path   = ["system", "time-zone"]
  value  = "UTC"
  values = ["UTC"]
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?i)invalid attribute combination`),
			},
			{
				Config: e.config(`
resource "edgeos_config" "x" {
  path = ["system", "time-zone"]
  tree = jsonencode({ interval = 5 })
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`quote numbers`),
			},
			{
				Config: e.config(`
resource "edgeos_config" "x" {
  path  = ["system", "time-zone"]
  value = "Europe/Berlin"
}`),
				ExpectError: regexp.MustCompile(`(?s)already exists.*system time-zone`),
			},
			{
				Config: e.config(`
resource "edgeos_config" "x" {
  path  = []
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`at least 1`),
			},
			{
				Config: e.config(`
resource "edgeos_config" "x" {
  path  = ["firewall", "name", "X", "rule", "1", "action"]
  value = "explode"
}`),
				ExpectError: reRejected,
			},
		},
	})
}

func TestAccConfigDataSource(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: e.config(`
data "edgeos_config" "host" {
  path = ["system", "host-name"]
}
data "edgeos_config" "addr" {
  path = ["interfaces", "ethernet", "eth1", "address"]
}
data "edgeos_config" "lo" {
  path = ["interfaces", "loopback", "lo"]
}
data "edgeos_config" "eth" {
  path = ["interfaces", "ethernet"]
}
data "edgeos_config" "missing" {
  path = ["firewall", "name", "NOPE"]
}
output "ports" {
  value = data.edgeos_config.eth.children
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.edgeos_config.host", "kind", "value"),
				resource.TestCheckResourceAttr("data.edgeos_config.host", "value", "ubnt"),
				resource.TestCheckResourceAttr("data.edgeos_config.addr", "kind", "values"),
				resource.TestCheckResourceAttr("data.edgeos_config.addr", "values.0", "192.168.1.1/24"),
				resource.TestCheckResourceAttr("data.edgeos_config.lo", "kind", "flag"),
				resource.TestCheckResourceAttr("data.edgeos_config.eth", "kind", "tree"),
				resource.TestCheckResourceAttr("data.edgeos_config.eth", "children.#", "5"),
				resource.TestCheckResourceAttr("data.edgeos_config.eth", "children.0", "eth0"),
				resource.TestCheckResourceAttrSet("data.edgeos_config.eth", "tree"),
				resource.TestCheckResourceAttr("data.edgeos_config.missing", "exists", "false"),
				resource.TestCheckNoResourceAttr("data.edgeos_config.missing", "kind"),
			),
		}},
	})
}

func TestAccSystemInfoDataSource(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: e.config(`data "edgeos_system_info" "this" {}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.edgeos_system_info.this", "version", "v3.0.1"),
				resource.TestCheckResourceAttr("data.edgeos_system_info.this", "platform", "e50"),
				resource.TestCheckResourceAttr("data.edgeos_system_info.this", "models", "EdgeRouter X, X SFP, 10X"),
				resource.TestCheckResourceAttr("data.edgeos_system_info.this", "host_name", "ubnt"),
				resource.TestCheckNoResourceAttr("data.edgeos_system_info.this", "domain_name"),
			),
		}},
	})
}
