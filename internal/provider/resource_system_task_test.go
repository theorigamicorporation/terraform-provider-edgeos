// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestCheckTaskInterval(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]string{
		"5m": "", "5": "", "59m": "", "1h": "", "23h": "", "1d": "", "31d": "",
		"60m": "less than 60", "60": "less than 60", "24h": "less than 24", "32d": "less than 32",
		"0m": "at least 1", "": "such as 5m", "5s": "such as 5m", "1w": "such as 5m", "m": "such as 5m",
		"-5m": "such as 5m", "5 m": "such as 5m",
	} {
		got := checkTaskInterval(s)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("checkTaskInterval(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestCheckCrontabSpec(t *testing.T) {
	t.Parallel()
	for s, ok := range map[string]bool{
		"*/15 * * * *": true, "30 3 * * 1-5": true, "0 0 1 jan,jul *": true, "@daily": true, "@reboot": true,
		"* * * *": false, "* * * * * *": false, "*  * * * *": false, "@often": false, "0 0 * * ?": false, "": false,
	} {
		if (checkCrontabSpec(s) == "") != ok {
			t.Errorf("checkCrontabSpec(%q) = %q", s, checkCrontabSpec(s))
		}
	}
}

func TestFuncValidator(t *testing.T) {
	t.Parallel()
	v := funcValidator{desc: "must be an interval", title: "Invalid interval", check: checkTaskInterval}
	for _, tc := range []struct {
		val  types.String
		fail bool
	}{
		{types.StringValue("5m"), false},
		{types.StringValue("90m"), true},
		{types.StringNull(), false},
		{types.StringUnknown(), false},
	} {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: tc.val}, resp)
		if resp.Diagnostics.HasError() != tc.fail {
			t.Errorf("%v: %v", tc.val, resp.Diagnostics)
		}
	}
	if v.MarkdownDescription(context.Background()) != "must be an interval" {
		t.Error("description")
	}
}

func TestSystemTaskImport(t *testing.T) {
	t.Parallel()
	r, ok := newSystemTaskResource()().(*leafResource[systemTaskModel])
	if !ok {
		t.Fatal("not a leaf resource")
	}
	attrs, err := r.spec.ImportAttrs("asus-ddns")
	if name, _ := attrs["name"].(types.String); err != nil || name.ValueString() != "asus-ddns" {
		t.Errorf("import: %v %v", attrs, err)
	}
	if _, err := r.spec.ImportAttrs("system task-scheduler task x"); err == nil {
		t.Error("a CLI path is not a task name")
	}
}

const taskDDNS = `
resource "edgeos_system_task_scheduler_task" "ddns" {
  name       = "asus-ddns"
  interval   = "5m"
  executable = "/config/scripts/asus-ddns.sh"
}`

const taskDDNSCron = `
resource "edgeos_system_task_scheduler_task" "ddns" {
  name         = "asus-ddns"
  crontab_spec = "*/10 * * * *"
  executable   = "/config/scripts/asus-ddns.sh"
  arguments    = "--quiet eth0"
}`

func TestAccSystemTask(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system task-scheduler task asus-ddns", "system task-scheduler task backup"),
			// A task Terraform does not manage stays.
			e.checkValue("system task-scheduler task other interval", "1d"),
		),
		Steps: []resource.TestStep{
			{
				Config: e.config(taskDDNS),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_task_scheduler_task.ddns", "id", "asus-ddns"),
					resource.TestCheckNoResourceAttr("edgeos_system_task_scheduler_task.ddns", "crontab_spec"),
					e.checkValue("system task-scheduler task asus-ddns", map[string]any{
						"interval":   "5m",
						"executable": map[string]any{"path": "/config/scripts/asus-ddns.sh"},
					}),
				),
			},
			{
				ResourceName:      "edgeos_system_task_scheduler_task.ddns",
				ImportState:       true,
				ImportStateId:     "asus-ddns",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_system_task_scheduler_task.ddns",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "asus-ddns",
			},
			{
				ResourceName:  "edgeos_system_task_scheduler_task.ddns",
				ImportState:   true,
				ImportStateId: "asus ddns",
				ExpectError:   regexp.MustCompile(`Invalid import ID`),
			},
			{
				// Interval to crontab-spec plus arguments is an update in one commit.
				PreConfig: func() {
					e.set("system task-scheduler task other interval", configtree.Single("1d"))()
					e.set("system task-scheduler task other executable path", configtree.Single("/config/scripts/other.sh"))()
				},
				Config: e.config(taskDDNSCron),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_task_scheduler_task.ddns", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("system task-scheduler task asus-ddns", map[string]any{
					"crontab-spec": "*/10 * * * *",
					"executable":   map[string]any{"path": "/config/scripts/asus-ddns.sh", "arguments": "--quiet eth0"},
				}),
			},
			{
				ResourceName:      "edgeos_system_task_scheduler_task.ddns",
				ImportState:       true,
				ImportStateId:     "asus-ddns",
				ImportStateVerify: true,
			},
			{
				// Drift: the schedule and the arguments changed by hand.
				PreConfig: func() {
					e.set("system task-scheduler task asus-ddns crontab-spec", configtree.Single("0 * * * *"))()
					e.del("system task-scheduler task asus-ddns executable arguments")()
				},
				Config: e.config(taskDDNSCron),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_task_scheduler_task.ddns", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system task-scheduler task asus-ddns crontab-spec", "*/10 * * * *"),
					e.checkValue("system task-scheduler task asus-ddns executable arguments", "--quiet eth0"),
				),
			},
			{
				// Drift: the task is deleted by hand and recreated.
				PreConfig: e.del("system task-scheduler task asus-ddns"),
				Config:    e.config(taskDDNS),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_task_scheduler_task.ddns", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("system task-scheduler task asus-ddns interval", "5m"),
			},
			{
				// Renaming replaces the task.
				Config: e.config(`
resource "edgeos_system_task_scheduler_task" "ddns" {
  name       = "backup"
  interval   = "1h"
  executable = "/config/scripts/backup.sh"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_task_scheduler_task.ddns", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("system task-scheduler task asus-ddns"),
					e.checkValue("system task-scheduler task backup interval", "1h"),
				),
			},
		},
	})
}

func TestAccSystemTaskErrors(t *testing.T) {
	e := newAccEnv(t)
	e.srv.Set(cliPath("system task-scheduler task asus-ddns interval"), configtree.Single("1h"))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// A task of that name already exists.
				Config:      e.config(taskDDNS),
				ExpectError: regexp.MustCompile(`(?s)Already exists.*asus-ddns`),
			},
			{
				PreConfig: func() {
					e.del("system task-scheduler")()
					e.srv.FailNextCommit("crond: reload failed")
				},
				Config:      e.config(taskDDNS),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*crond: reload failed`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("system task-scheduler")); ok {
		t.Fatal("task created after a failed commit")
	}
}

func TestAccSystemTaskValidation(t *testing.T) {
	e := newAccEnv(t)
	const exe = "\n  executable = \"/config/scripts/x.sh\"\n"
	for body, want := range map[string]string{
		`name = "t"` + exe: `Invalid Attribute Combination`,
		`name = "t"` + exe + `interval = "5m"` + "\n" + `crontab_spec = "@daily"`:  `Invalid Attribute Combination`,
		`name = "t"` + exe + `interval = "60m"`:                                    `less than 60`,
		`name = "t"` + exe + `interval = "5s"`:                                     `such as 5m`,
		`name = "t"` + exe + `crontab_spec = "* * *"`:                              `five cron fields`,
		`name = "t" ` + "\n" + `interval = "5m"` + "\n" + `executable = "x.sh"`:    `absolute path`,
		`name = "t"` + exe + `interval = "5m"` + "\n" + `arguments = "say \"hi\""`: `double quote`,
		`name = "t t"` + exe + `interval = "5m"`:                                   `letters, digits`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_system_task_scheduler_task\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
