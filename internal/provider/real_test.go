// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// Tests in this file run against a REAL router and WRITE to it. They create,
// change and delete config under names starting with TFACC, and EdgeOS saves
// every commit to config.boot. Run them only against a lab router:
//
//	EDGEOS_ACC_REAL=1 EDGEOS_HOST=... EDGEOS_USERNAME=... EDGEOS_PASSWORD=... \
//	EDGEOS_INSECURE=true just testacc-real
//
// They are skipped unless EDGEOS_ACC_REAL=1 and never run in CI.

func realRouter(t *testing.T) {
	t.Helper()
	if os.Getenv("EDGEOS_ACC_REAL") != "1" {
		t.Skip("set EDGEOS_ACC_REAL=1 to run tests that write to a real router")
	}
	for _, v := range []string{"EDGEOS_HOST", "EDGEOS_USERNAME", "EDGEOS_PASSWORD"} {
		if os.Getenv(v) == "" {
			t.Fatalf("%s must be set for real router tests", v)
		}
	}
}

func TestAccRealSystemInfo(t *testing.T) {
	realRouter(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "edgeos_system_info" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.edgeos_system_info.this", "version"),
				resource.TestCheckResourceAttrSet("data.edgeos_system_info.this", "host_name"),
			),
		}},
	})
}

func TestAccRealFirewall(t *testing.T) {
	realRouter(t)
	cfg := func(port string) string {
		return `
resource "edgeos_firewall_name" "test" {
  name        = "TFACC_TEST"
  description = "terraform-provider-edgeos acceptance test"
}

resource "edgeos_firewall_rule" "established" {
  ruleset = edgeos_firewall_name.test.name
  number  = 10
  action  = "accept"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "port" {
  ruleset  = edgeos_firewall_name.test.name
  number   = 20
  action   = "drop"
  protocol = "tcp"
  destination = {
    address = "192.0.2.10"
    port    = "` + port + `"
  }
}
`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("22"),
				Check:  resource.TestCheckResourceAttr("edgeos_firewall_rule.port", "destination.port", "22"),
			},
			{
				Config: cfg("2222"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_firewall_rule.port", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("edgeos_firewall_rule.port", "destination.port", "2222"),
			},
			{
				ResourceName:      "edgeos_firewall_rule.port",
				ImportState:       true,
				ImportStateId:     "TFACC_TEST:20",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccRealConfig(t *testing.T) {
	realRouter(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "edgeos_config" "task" {
  path = ["system", "task-scheduler", "task", "tfacc-test"]
  tree = jsonencode({
    interval   = "1d"
    executable = { path = "/bin/true" }
  })
}`,
			},
			{
				ResourceName:      "edgeos_config.task",
				ImportState:       true,
				ImportStateId:     "system task-scheduler task tfacc-test",
				ImportStateVerify: true,
			},
		},
	})
}
