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

func facilityMap(kv ...string) types.Map {
	elems := map[string]attr.Value{}
	for i := 0; i+1 < len(kv); i += 2 {
		elems[kv[i]] = types.StringValue(kv[i+1])
	}
	return types.MapValueMust(types.StringType, elems)
}

func TestSystemSyslogMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		model  systemSyslogModel
		leaves map[string]configtree.Value
	}{
		{"nothing", systemSyslogModel{
			Facilities: types.MapNull(types.StringType), ArchiveFiles: types.Int64Null(), ArchiveSize: types.Int64Null(),
		}, map[string]configtree.Value{}},
		{"defaults and archive", systemSyslogModel{
			Facilities:   facilityMap("all", "notice", "protocols", "debug"),
			ArchiveFiles: types.Int64Value(5),
			ArchiveSize:  types.Int64Value(250),
		}, map[string]configtree.Value{
			"facility all level":       configtree.Single("notice"),
			"facility protocols level": configtree.Single("debug"),
			"archive files":            configtree.Single("5"),
			"archive size":             configtree.Single("250"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := systemSyslogToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := systemSyslogModel{} // ID null: as after an import
			systemSyslogFromTree(ctx, configtree.Build(l), &back, &diags)
			assertLeaves(t, systemSyslogToLeaves(ctx, &back, &diags), tc.leaves)
			if diags.HasError() {
				t.Fatal(diags)
			}
		})
	}
}

func TestSystemSyslogFromTree(t *testing.T) {
	t.Parallel()
	tree := map[string]any{
		"facility": map[string]any{"all": map[string]any{"level": "notice"}, "kern": nil},
		"archive":  map[string]any{"files": "5"},
	}
	m := systemSyslogModel{ID: types.StringValue(systemSyslogID), Facilities: facilityMap("all", "info")}
	var diags diag.Diagnostics
	systemSyslogFromTree(context.Background(), tree, &m, &diags)
	// A facility without a level reads as "" so the next plan sets one.
	if !m.Facilities.Equal(facilityMap("all", "notice", "kern", "")) {
		t.Errorf("facilities = %v", m.Facilities)
	}
	if !m.ArchiveFiles.IsNull() || !m.ArchiveSize.IsNull() {
		t.Errorf("unmanaged archive settings were read: %v %v", m.ArchiveFiles, m.ArchiveSize)
	}
	if got := getFacilities(map[string]any{}); !got.IsNull() {
		t.Errorf("no facility node must read as null, got %v", got)
	}
	if facilitiesOwned(types.MapNull(types.StringType)) != nil {
		t.Error("unset facilities must not own the facility list")
	}
	systemSyslogFromTree(context.Background(), map[string]any{"archive": map[string]any{"size": "big"}},
		&systemSyslogModel{}, &diags)
	if !diags.HasError() {
		t.Error("a non-numeric archive size must be an error")
	}
}

func TestValidSyslogHost(t *testing.T) {
	t.Parallel()
	for s, ok := range map[string]bool{
		"192.0.2.10": true, "192.0.2.10:5140": true, "syslog.example.net": true, "syslog.example.net:514": true,
		"2001:db8::10": true, "[2001:db8::10]:5140": true, "log01": true,
		"": false, "-log": false, "log host": false, "syslog.example.net:0": false, "syslog.example.net:70000": false,
		"192.0.2.10:0": false, "[2001:db8::10]": false, "syslog.example.net:": false,
	} {
		if validSyslogHost(s) != ok {
			t.Errorf("validSyslogHost(%q) = %v", s, !ok)
		}
	}
}

const syslogDefaults = `
resource "edgeos_system_syslog" "this" {
  facilities = {
    all       = "notice"
    protocols = "debug"
  }
}`

func TestAccSystemSyslog(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system syslog global facility", "system syslog global archive"),
			// Hosts are separate resources and stay.
			e.checkValue("system syslog host 192.0.2.10 facility all level", "info"),
		),
		Steps: []resource.TestStep{
			{
				// Adopt the EdgeOS defaults without changes.
				Config: e.config(syslogDefaults),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_syslog.this", "id", "syslog"),
					resource.TestCheckResourceAttr("edgeos_system_syslog.this", "facilities.protocols", "debug"),
					e.checkValue("system syslog global", map[string]any{"facility": map[string]any{
						"all":       map[string]any{"level": "notice"},
						"protocols": map[string]any{"level": "debug"},
					}}),
				),
			},
			{
				ResourceName:      "edgeos_system_syslog.this",
				ImportState:       true,
				ImportStateId:     "syslog",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_system_syslog.this",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "syslog",
			},
			{
				ResourceName:  "edgeos_system_syslog.this",
				ImportState:   true,
				ImportStateId: "system syslog",
				ExpectError:   regexp.MustCompile(`only valid ID is "syslog"`),
			},
			{
				// Change a level, drop a facility, add the archive settings.
				// A remote host added by hand is not touched.
				PreConfig: e.set("system syslog host 192.0.2.10 facility all level", configtree.Single("info")),
				Config: e.config(`
resource "edgeos_system_syslog" "this" {
  facilities = {
    all  = "info"
    kern = "warning"
  }
  archive_files = 10
  archive_size  = 1024
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system syslog global", map[string]any{
						"facility": map[string]any{
							"all":  map[string]any{"level": "info"},
							"kern": map[string]any{"level": "warning"},
						},
						"archive": map[string]any{"files": "10", "size": "1024"},
					}),
					e.checkValue("system syslog host 192.0.2.10 facility all level", "info"),
				),
			},
			{
				// Drift: a facility added and a level changed by hand.
				PreConfig: func() {
					e.set("system syslog global facility mail level", configtree.Single("err"))()
					e.set("system syslog global facility kern level", configtree.Single("debug"))()
				},
				Config: e.config(`
resource "edgeos_system_syslog" "this" {
  facilities = {
    all  = "info"
    kern = "warning"
  }
  archive_files = 10
  archive_size  = 1024
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("system syslog global facility mail"),
					e.checkValue("system syslog global facility kern level", "warning"),
				),
			},
			{
				Config: e.config(syslogDefaults),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("system syslog global archive"),
					e.checkAbsent("system syslog global facility kern"),
					e.checkValue("system syslog global facility protocols level", "debug"),
				),
			},
		},
	})
}

func TestAccSystemSyslogHost(t *testing.T) {
	e := newAccEnv(t)
	const cfg = `
resource "edgeos_system_syslog_host" "siem" {
  host = "192.0.2.10"
  facilities = {
    all = "info"
  }
}`
	const updated = `
resource "edgeos_system_syslog_host" "siem" {
  host = "192.0.2.10"
  facilities = {
    all       = "warning"
    authpriv  = "info"
    protocols = "debug"
  }
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system syslog host"),
			e.checkValue("system syslog global facility all level", "notice"),
		),
		Steps: []resource.TestStep{
			{
				Config: e.config(cfg),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_syslog_host.siem", "id", "192.0.2.10"),
					e.checkValue("system syslog host 192.0.2.10", map[string]any{
						"facility": map[string]any{"all": map[string]any{"level": "info"}},
					}),
				),
			},
			{
				Config: e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog_host.siem", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("system syslog host 192.0.2.10 facility", map[string]any{
					"all":       map[string]any{"level": "warning"},
					"authpriv":  map[string]any{"level": "info"},
					"protocols": map[string]any{"level": "debug"},
				}),
			},
			{
				ResourceName:      "edgeos_system_syslog_host.siem",
				ImportState:       true,
				ImportStateId:     "192.0.2.10",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_system_syslog_host.siem",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "192.0.2.10",
			},
			{
				ResourceName:  "edgeos_system_syslog_host.siem",
				ImportState:   true,
				ImportStateId: "log host",
				ExpectError:   regexp.MustCompile(`Invalid import ID`),
			},
			{
				// Drift: a facility added by hand is removed.
				PreConfig: e.set("system syslog host 192.0.2.10 facility kern level", configtree.Single("err")),
				Config:    e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog_host.siem", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkAbsent("system syslog host 192.0.2.10 facility kern"),
			},
			{
				// Drift: the host is deleted by hand and recreated.
				PreConfig: e.del("system syslog host 192.0.2.10"),
				Config:    e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog_host.siem", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("system syslog host 192.0.2.10 facility all level", "warning"),
			},
			{
				// A new host with a port replaces the old one.
				Config: e.config(`
resource "edgeos_system_syslog_host" "siem" {
  host = "syslog.example.net:5140"
  facilities = {
    all = "info"
  }
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog_host.siem", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("system syslog host 192.0.2.10"),
					e.checkValue("system syslog host syslog.example.net:5140 facility all level", "info"),
				),
			},
			{
				ResourceName:      "edgeos_system_syslog_host.siem",
				ImportState:       true,
				ImportStateId:     "syslog.example.net:5140",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccSystemSyslogHostExists(t *testing.T) {
	e := newAccEnv(t)
	e.srv.Set(cliPath("system syslog host 192.0.2.10 facility all level"), configtree.Single("err"))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: e.config(`
resource "edgeos_system_syslog_host" "siem" {
  host       = "192.0.2.10"
  facilities = { all = "info" }
}`),
			ExpectError: regexp.MustCompile(`(?s)Already exists.*192.0.2.10`),
		}},
	})
}

func TestAccSystemSyslogFile(t *testing.T) {
	e := newAccEnv(t)
	const cfg = `
resource "edgeos_system_syslog_file" "fw" {
  name = "firewall"
  facilities = {
    kern = "warning"
  }
  archive_files = 3
  archive_size  = 512
}`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("system syslog file"),
		Steps: []resource.TestStep{
			{
				Config: e.config(cfg),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_syslog_file.fw", "id", "firewall"),
					e.checkValue("system syslog file firewall", map[string]any{
						"facility": map[string]any{"kern": map[string]any{"level": "warning"}},
						"archive":  map[string]any{"files": "3", "size": "512"},
					}),
				),
			},
			{
				ResourceName:      "edgeos_system_syslog_file.fw",
				ImportState:       true,
				ImportStateId:     "firewall",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_system_syslog_file.fw",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "firewall",
			},
			{
				ResourceName:  "edgeos_system_syslog_file.fw",
				ImportState:   true,
				ImportStateId: "/var/log/x",
				ExpectError:   regexp.MustCompile(`Invalid import ID`),
			},
			{
				// Drift: archive size changed by hand. Dropping archive_files
				// from the config removes it.
				PreConfig: e.set("system syslog file firewall archive size", configtree.Single("64")),
				Config: e.config(`
resource "edgeos_system_syslog_file" "fw" {
  name = "firewall"
  facilities = {
    kern = "warning"
  }
  archive_size = 512
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_syslog_file.fw", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("system syslog file firewall archive", map[string]any{"size": "512"}),
			},
		},
	})
}

func TestAccSystemSyslogErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			PreConfig:   func() { e.srv.FailNextCommit("rsyslogd: restart failed") },
			Config:      e.config(`resource "edgeos_system_syslog" "x" { archive_files = 5 }`),
			ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*rsyslogd: restart failed`),
		}},
	})
	if _, ok := e.srv.Get(cliPath("system syslog global archive")); ok {
		t.Fatal("archive set after a failed commit")
	}
}

func TestAccSystemSyslogValidation(t *testing.T) {
	e := newAccEnv(t)
	for cfg, want := range map[string]string{
		`resource "edgeos_system_syslog" "x" {}`:                                       `Nothing to manage`,
		`resource "edgeos_system_syslog" "x" { facilities = {} }`:                      `at least 1`,
		`resource "edgeos_system_syslog" "x" { facilities = { everything = "info" } }`: `value must be one of`,
		`resource "edgeos_system_syslog" "x" { facilities = { all = "verbose" } }`:     `value must be one of`,
		`resource "edgeos_system_syslog" "x" { archive_files = 0 }`:                    `between 1 and`,
		`resource "edgeos_system_syslog_host" "x" {
  host       = "log host"
  facilities = { all = "info" }
}`: `Invalid syslog host`,
		`resource "edgeos_system_syslog_host" "x" { host = "192.0.2.10" }`: `"facilities" is required`,
		`resource "edgeos_system_syslog_file" "x" {
  name       = "../messages"
  facilities = { all = "info" }
}`: `letters, digits`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config(cfg),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
