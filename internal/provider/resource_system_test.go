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

func nullSystem() systemModel {
	return systemModel{
		HostName: types.StringNull(), DomainName: types.StringNull(), TimeZone: types.StringNull(),
		NameServers: types.ListNull(types.StringType), NTPServers: types.SetNull(types.StringType),
		SendAnalyticsReport: types.BoolNull(), SendCrashReport: types.BoolNull(),
	}
}

func TestSystemMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	full := systemModel{
		HostName:    types.StringValue("gw01"),
		DomainName:  types.StringValue("example.net"),
		TimeZone:    types.StringValue("Europe/Amsterdam"),
		NameServers: stringList([]string{"198.51.100.53", "192.0.2.53", "2001:db8::53"}),
		NTPServers:  stringSet([]string{"192.0.2.123", "ntp.example.net"}),

		SendAnalyticsReport: types.BoolValue(false),
		SendCrashReport:     types.BoolValue(true),
	}
	cases := []struct {
		name   string
		model  systemModel
		leaves map[string]configtree.Value
	}{
		{"nothing managed", nullSystem(), map[string]configtree.Value{}},
		{"everything", full, map[string]configtree.Value{
			"host-name":                  configtree.Single("gw01"),
			"domain-name":                configtree.Single("example.net"),
			"time-zone":                  configtree.Single("Europe/Amsterdam"),
			"name-server":                configtree.Multi("198.51.100.53", "192.0.2.53", "2001:db8::53"),
			"ntp server 192.0.2.123":     configtree.Flag(),
			"ntp server ntp.example.net": configtree.Flag(),
			"analytics-handler send-analytics-report": configtree.Single("false"),
			"crash-handler send-crash-report":         configtree.Single("true"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := systemToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)
			back := nullSystem()
			back.ID = types.StringNull() // as after an import
			systemFromTree(ctx, configtree.Build(l), &back, &diags)
			assertLeaves(t, systemToLeaves(ctx, &back, &diags), tc.leaves)
			if len(tc.leaves) > 0 && !back.NameServers.Equal(tc.model.NameServers) {
				t.Errorf("name server order lost: %v", back.NameServers)
			}
		})
	}
}

func TestSystemFromTreeReadsOnlyManagedAttributes(t *testing.T) {
	t.Parallel()
	tree := map[string]any{
		"host-name": "r1", "time-zone": "UTC", "name-server": []any{"192.0.2.53"},
		"ntp":           map[string]any{"server": map[string]any{"a.example": nil, "b.example": map[string]any{"prefer": nil}}},
		"login":         map[string]any{"user": map[string]any{"admin": map[string]any{"level": "admin"}}},
		"crash-handler": map[string]any{"send-crash-report": "false"},
	}
	m := nullSystem()
	m.ID = types.StringValue(systemID)
	m.HostName = types.StringValue("r0")
	m.NTPServers = stringSet([]string{"a.example"})
	systemFromTree(context.Background(), tree, &m, nil)
	if m.HostName.ValueString() != "r1" || !m.NTPServers.Equal(stringSet([]string{"a.example", "b.example"})) {
		t.Errorf("managed attributes not read: %+v", m)
	}
	if !m.TimeZone.IsNull() || !m.NameServers.IsNull() || !m.DomainName.IsNull() || !m.SendCrashReport.IsNull() {
		t.Errorf("unmanaged attributes were read: %+v", m)
	}
}

func TestSystemOwnedAndImport(t *testing.T) {
	t.Parallel()
	m := nullSystem()
	if len(systemOwned(&m)) != 0 {
		t.Error("unset ntp_servers must not own the server list")
	}
	m.NTPServers = stringSet(nil)
	if got := systemOwned(&m); len(got) != 1 || !got[0].Equal(configtree.NewPath("ntp", "server", "*")) {
		t.Errorf("owned = %v", got)
	}
	attrs, err := systemImport("system")
	if err != nil || len(attrs) != 1 || !attrs["id"].IsNull() {
		t.Errorf("import system: %v %v", attrs, err)
	}
	if _, err := systemImport("router"); err == nil {
		t.Error("only \"system\" is a valid ID")
	}
}

func TestIPAddressValidator(t *testing.T) {
	t.Parallel()
	for s, ok := range map[string]bool{"192.0.2.53": true, "2001:db8::53": true, "192.0.2.0/24": false, "dns": false} {
		resp := &validator.StringResponse{}
		ipAddress().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(s)}, resp)
		if resp.Diagnostics.HasError() == ok {
			t.Errorf("%q: %v", s, resp.Diagnostics)
		}
	}
	resp := &validator.StringResponse{}
	ipAddress().ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringNull()}, resp)
	if resp.Diagnostics.HasError() || ipAddress().MarkdownDescription(context.Background()) == "" {
		t.Error("null values must pass and the validator needs a description")
	}
}

// Acceptance tests against the fake router.

const systemConfig = `
resource "edgeos_system" "this" {
  host_name    = "gw01"
  domain_name  = "example.net"
  time_zone    = "Europe/Amsterdam"
  name_servers = ["198.51.100.53", "192.0.2.53"]
  ntp_servers  = ["0.ubnt.pool.ntp.org", "192.0.2.123"]
}`

func TestAccSystem(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system host-name", "system domain-name", "system time-zone", "system name-server", "system ntp"),
			// Everything else in system is untouched.
			e.checkValue("system login user admin level", "admin"),
			e.checkValue("system syslog global facility all level", "notice"),
		),
		Steps: []resource.TestStep{
			{
				// Adopt: 1.ubnt.pool.ntp.org is dropped, 0.ubnt keeps its
				// prefer option because it stays in the list.
				PreConfig: e.set("system ntp server 0.ubnt.pool.ntp.org prefer", configtree.Flag()),
				Config:    e.config(systemConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system.this", "id", "system"),
					resource.TestCheckResourceAttr("edgeos_system.this", "name_servers.0", "198.51.100.53"),
					e.checkValue("system host-name", "gw01"),
					e.checkValue("system domain-name", "example.net"),
					e.checkValue("system time-zone", "Europe/Amsterdam"),
					e.checkValue("system name-server", []string{"198.51.100.53", "192.0.2.53"}),
					e.checkValue("system ntp server", map[string]any{
						"0.ubnt.pool.ntp.org": map[string]any{"prefer": nil},
						"192.0.2.123":         nil,
					}),
				),
			},
			{
				// Reordering the name servers is an update; so is dropping
				// the domain and an NTP server.
				Config: e.config(`
resource "edgeos_system" "this" {
  host_name    = "gw02"
  time_zone    = "Europe/Amsterdam"
  name_servers = ["192.0.2.53", "198.51.100.53"]
  ntp_servers  = ["192.0.2.123"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system host-name", "gw02"),
					e.checkAbsent("system domain-name"),
					e.checkValue("system name-server", []string{"192.0.2.53", "198.51.100.53"}),
					e.checkValue("system ntp server", map[string]any{"192.0.2.123": nil}),
				),
			},
			{
				ResourceName:      "edgeos_system.this",
				ImportState:       true,
				ImportStateId:     "system",
				ImportStateVerify: true,
			},
			{
				// Drift: host name, NTP servers and name servers changed by hand.
				PreConfig: func() {
					e.set("system host-name", configtree.Single("hand-edit"))()
					e.set("system ntp server pool.example.org", configtree.Flag())()
					e.del("system name-server")()
				},
				Config: e.config(`
resource "edgeos_system" "this" {
  host_name    = "gw02"
  time_zone    = "Europe/Amsterdam"
  name_servers = ["192.0.2.53", "198.51.100.53"]
  ntp_servers  = ["192.0.2.123"]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system host-name", "gw02"),
					e.checkValue("system name-server", []string{"192.0.2.53", "198.51.100.53"}),
					e.checkValue("system ntp server", map[string]any{"192.0.2.123": nil}),
				),
			},
			{
				// The report switches are optional and only managed when set.
				PreConfig: e.set("system crash-handler send-crash-report", configtree.Single("true")),
				Config: e.config(`
resource "edgeos_system" "this" {
  host_name             = "gw02"
  time_zone             = "Europe/Amsterdam"
  name_servers          = ["192.0.2.53", "198.51.100.53"]
  ntp_servers           = ["192.0.2.123"]
  send_analytics_report = false
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system analytics-handler send-analytics-report", "false"),
					e.checkValue("system crash-handler send-crash-report", "true"),
					resource.TestCheckNoResourceAttr("edgeos_system.this", "send_crash_report"),
				),
			},
			{
				// Drift on a report switch is corrected.
				PreConfig: e.set("system analytics-handler send-analytics-report", configtree.Single("true")),
				Config: e.config(`
resource "edgeos_system" "this" {
  host_name             = "gw02"
  time_zone             = "Europe/Amsterdam"
  name_servers          = ["192.0.2.53", "198.51.100.53"]
  ntp_servers           = ["192.0.2.123"]
  send_analytics_report = false
  send_crash_report     = false
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system analytics-handler send-analytics-report", "false"),
					e.checkValue("system crash-handler send-crash-report", "false"),
				),
			},
			{
				// Back to the full config before destroy. Dropping the
				// switches removes them.
				Config: e.config(systemConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system domain-name", "example.net"),
					e.checkAbsent("system analytics-handler"),
					e.checkAbsent("system crash-handler"),
				),
			},
		},
	})
}

func TestAccSystemErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("ntpd: restart failed") },
				Config:      e.config(systemConfig),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*ntpd: restart failed`),
			},
		},
	})
	if got, _ := e.srv.Get(cliPath("system host-name")); got != "ubnt" {
		t.Fatalf("host-name changed after a failed commit: %v", got)
	}
}

func TestAccSystemValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		`host_name = "gw01.example.net"`:                  `host name of up to 63`,
		`host_name = "-gw"`:                               `host name of up to 63`,
		`domain_name = "example..net"`:                    `domain name such as`,
		`time_zone = "Europe/Amsterdam \"x\""`:            `double quote`,
		`name_servers = ["192.0.2.53", "192.0.2.53"]`:     `Duplicate List Value`,
		`name_servers = ["dns.example.net"]`:              `Invalid IP address`,
		`name_servers = []`:                               `at least 1`,
		`ntp_servers = ["pool ntp"]`:                      `host name or IP address`,
		`ntp_servers = []`:                                `at least 1`,
		`host_name = "gw"` + "\n" + `bogus_attribute = 1`: `Unsupported argument`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_system\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
