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

func nullOffload() systemOffloadModel {
	var m systemOffloadModel
	for _, k := range offloadKnobs {
		*k.get(&m) = types.BoolNull()
	}
	return m
}

func TestSystemOffloadMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cavium := nullOffload()
	cavium.IPv4Forwarding = types.BoolValue(true)
	cavium.IPv4VLAN = types.BoolValue(true)
	cavium.IPv4PPPoE = types.BoolValue(false)
	cavium.IPv4GRE = types.BoolValue(true)
	cavium.IPv4Bonding = types.BoolValue(true)
	cavium.IPv6Forwarding = types.BoolValue(true)
	cavium.IPv6VLAN = types.BoolValue(false)
	cavium.IPv6PPPoE = types.BoolValue(true)
	cavium.IPv6Bonding = types.BoolValue(false)
	cavium.IPsec = types.BoolValue(true)
	erx := nullOffload()
	erx.HWNAT = types.BoolValue(true)
	erx.IPsec = types.BoolValue(false)
	cases := []struct {
		name   string
		model  systemOffloadModel
		leaves map[string]configtree.Value
	}{
		{"nothing", nullOffload(), map[string]configtree.Value{}},
		{"er-x", erx, map[string]configtree.Value{
			"hwnat": configtree.Single("enable"),
			"ipsec": configtree.Single("disable"),
		}},
		{"cavium", cavium, map[string]configtree.Value{
			"ipsec":           configtree.Single("enable"),
			"ipv4 forwarding": configtree.Single("enable"),
			"ipv4 vlan":       configtree.Single("enable"),
			"ipv4 pppoe":      configtree.Single("disable"),
			"ipv4 gre":        configtree.Single("enable"),
			"ipv4 bonding":    configtree.Single("enable"),
			"ipv6 forwarding": configtree.Single("enable"),
			"ipv6 vlan":       configtree.Single("disable"),
			"ipv6 pppoe":      configtree.Single("enable"),
			"ipv6 bonding":    configtree.Single("disable"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := systemOffloadToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := nullOffload() // ID null: as after an import
			systemOffloadFromTree(ctx, configtree.Build(l), &back, &diags)
			assertLeaves(t, systemOffloadToLeaves(ctx, &back, &diags), tc.leaves)
			if diags.HasError() {
				t.Fatal(diags)
			}
		})
	}
}

func TestSystemOffloadReadsOnlyManagedAttributes(t *testing.T) {
	t.Parallel()
	tree := map[string]any{"hwnat": "disable", "ipv4": map[string]any{"forwarding": "enable"}}
	m := nullOffload()
	m.ID = types.StringValue(systemOffloadID)
	m.HWNAT = types.BoolValue(true)
	systemOffloadFromTree(context.Background(), tree, &m, nil)
	if !m.HWNAT.Equal(types.BoolValue(false)) || !m.IPv4Forwarding.IsNull() {
		t.Errorf("hwnat = %v, ipv4_forwarding = %v", m.HWNAT, m.IPv4Forwarding)
	}
}

func TestImportReadingAll(t *testing.T) {
	t.Parallel()
	attrs, err := importReadingAll("offload")("offload")
	if err != nil || len(attrs) != 1 || !attrs["id"].IsNull() {
		t.Errorf("import offload: %v %v", attrs, err)
	}
	if _, err := importReadingAll("offload")("system offload"); err == nil {
		t.Error("only \"offload\" is a valid ID")
	}
}

const offloadERX = `
resource "edgeos_system_offload" "this" {
  hwnat = true
}`

func TestAccSystemOffload(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system offload hwnat", "system offload ipsec"),
			// Knobs set outside Terraform stay.
			e.checkValue("system offload ipv4 forwarding", "enable"),
		),
		Steps: []resource.TestStep{
			{
				// The router has no system offload node yet.
				PreConfig: func() {
					if _, ok := e.srv.Get(cliPath("system offload")); ok {
						t.Fatal("default config has system offload")
					}
				},
				Config: e.config(offloadERX),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_offload.this", "id", "offload"),
					resource.TestCheckResourceAttr("edgeos_system_offload.this", "hwnat", "true"),
					resource.TestCheckNoResourceAttr("edgeos_system_offload.this", "ipsec"),
					e.checkValue("system offload", map[string]any{"hwnat": "enable"}),
				),
			},
			{
				ResourceName:      "edgeos_system_offload.this",
				ImportState:       true,
				ImportStateId:     "offload",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_system_offload.this",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				ImportStateId:   "offload",
			},
			{
				ResourceName:  "edgeos_system_offload.this",
				ImportState:   true,
				ImportStateId: "system offload",
				ExpectError:   regexp.MustCompile(`only valid ID is "offload"`),
			},
			{
				// A knob Terraform does not manage is left alone.
				PreConfig: e.set("system offload ipv4 forwarding", configtree.Single("enable")),
				Config: e.config(`
resource "edgeos_system_offload" "this" {
  hwnat = false
  ipsec = true
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_offload.this", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("system offload", map[string]any{
					"hwnat": "disable", "ipsec": "enable", "ipv4": map[string]any{"forwarding": "enable"},
				}),
			},
			{
				// Drift: hwnat switched on and ipsec removed by hand.
				PreConfig: func() {
					e.set("system offload hwnat", configtree.Single("enable"))()
					e.del("system offload ipsec")()
				},
				Config: e.config(`
resource "edgeos_system_offload" "this" {
  hwnat = false
  ipsec = true
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_offload.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system offload hwnat", "disable"),
					e.checkValue("system offload ipsec", "enable"),
				),
			},
			{
				// Dropping an attribute removes the node.
				Config: e.config(offloadERX),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system offload hwnat", "enable"),
					e.checkAbsent("system offload ipsec"),
				),
			},
		},
	})
}

func TestAccSystemOffloadCavium(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("system offload"),
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_system_offload" "this" {
  ipv4_forwarding = true
  ipv4_vlan       = true
  ipv4_pppoe      = true
  ipv4_gre        = false
  ipv4_bonding    = true
  ipv6_forwarding = true
  ipv6_vlan       = true
  ipv6_pppoe      = false
  ipv6_bonding    = true
  ipsec           = true
}`),
				Check: e.checkValue("system offload", map[string]any{
					"ipsec": "enable",
					"ipv4": map[string]any{
						"forwarding": "enable", "vlan": "enable", "pppoe": "enable", "gre": "disable", "bonding": "enable",
					},
					"ipv6": map[string]any{
						"forwarding": "enable", "vlan": "enable", "pppoe": "disable", "bonding": "enable",
					},
				}),
			},
			{
				ResourceName:      "edgeos_system_offload.this",
				ImportState:       true,
				ImportStateId:     "offload",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccSystemOffloadErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("offload: reboot required") },
				Config:      e.config(offloadERX),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*reboot required`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("system offload")); ok {
		t.Fatal("system offload exists after a failed commit")
	}
}

func TestAccSystemOffloadValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		``:                   `Nothing to manage`,
		`hwnat = "maybe"`:    `a bool is required`,
		`ipv6_gre = true`:    `Unsupported argument`,
		`flow_lifetime = 30`: `Unsupported argument`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_system_offload\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
