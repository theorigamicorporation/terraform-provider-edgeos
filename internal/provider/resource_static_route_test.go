// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func hopValue(desc string, disabled bool, distance int64) attr.Value {
	d := types.StringNull()
	if desc != "" {
		d = types.StringValue(desc)
	}
	n := types.Int64Null()
	if distance > 0 {
		n = types.Int64Value(distance)
	}
	return types.ObjectValueMust(routeHopType.AttrTypes, map[string]attr.Value{
		"description": d, "disabled": types.BoolValue(disabled), "distance": n,
	})
}

func TestStaticRouteMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		model  staticRouteModel
		leaves map[string]configtree.Value
	}{
		{
			name: "one gateway without options",
			model: staticRouteModel{NextHops: types.MapValueMust(routeHopType, map[string]attr.Value{
				"192.168.1.254": hopValue("", false, 0),
			})},
			leaves: map[string]configtree.Value{"next-hop 192.168.1.254": configtree.Flag()},
		},
		{
			name: "two gateways and a blackhole",
			model: staticRouteModel{
				NextHops: types.MapValueMust(routeHopType, map[string]attr.Value{
					"192.168.1.254": hopValue("primary", false, 1),
					"192.168.2.254": hopValue("backup", true, 10),
				}),
				Blackhole: &routeHop{Description: types.StringValue("last resort"), Disabled: types.BoolValue(false), Distance: types.Int64Value(250)},
			},
			leaves: map[string]configtree.Value{
				"next-hop 192.168.1.254":             configtree.Flag(),
				"next-hop 192.168.1.254 description": configtree.Single("primary"),
				"next-hop 192.168.1.254 distance":    configtree.Single("1"),
				"next-hop 192.168.2.254":             configtree.Flag(),
				"next-hop 192.168.2.254 description": configtree.Single("backup"),
				"next-hop 192.168.2.254 disable":     configtree.Flag(),
				"next-hop 192.168.2.254 distance":    configtree.Single("10"),
				"blackhole":                          configtree.Flag(),
				"blackhole description":              configtree.Single("last resort"),
				"blackhole distance":                 configtree.Single("250"),
			},
		},
		{
			name:   "plain blackhole",
			model:  staticRouteModel{NextHops: types.MapNull(routeHopType), Blackhole: &routeHop{Description: types.StringNull(), Disabled: types.BoolValue(false), Distance: types.Int64Null()}},
			leaves: map[string]configtree.Value{"blackhole": configtree.Flag()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := staticRouteToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := staticRouteModel{}
			staticRouteFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, staticRouteToLeaves(ctx, &back, &diags), tc.leaves)
			if (tc.model.Blackhole == nil) != (back.Blackhole == nil) || tc.model.NextHops.IsNull() != back.NextHops.IsNull() {
				t.Fatalf("round trip changed shape: %+v", back)
			}
		})
	}
}

func TestStaticRouteFromTreeBadDistance(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	m := staticRouteModel{}
	staticRouteFromTree(context.Background(), map[string]any{"next-hop": map[string]any{"192.0.2.1": map[string]any{"distance": "far"}}}, &m, &diags)
	if !diags.HasError() {
		t.Fatal("a non-numeric distance should be reported")
	}
}

func TestStaticInterfaceRouteMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[staticInterfaceRouteModel](newStaticInterfaceRouteResource())
	m := staticInterfaceRouteModel{Interfaces: types.MapValueMust(routeHopType, map[string]attr.Value{
		"wg0":    hopValue("tunnel", false, 0),
		"pppoe0": hopValue("", true, 20),
	})}
	want := map[string]configtree.Value{
		"next-hop-interface wg0":             configtree.Flag(),
		"next-hop-interface wg0 description": configtree.Single("tunnel"),
		"next-hop-interface pppoe0":          configtree.Flag(),
		"next-hop-interface pppoe0 disable":  configtree.Flag(),
		"next-hop-interface pppoe0 distance": configtree.Single("20"),
	}
	var diags diag.Diagnostics
	l := spec.ToLeaves(ctx, &m, &diags)
	assertLeaves(t, l, want)
	back := staticInterfaceRouteModel{}
	spec.FromTree(ctx, configtree.Build(l), &back, &diags)
	assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), want)
	if diags.HasError() {
		t.Fatal(diags)
	}
	empty := staticInterfaceRouteModel{}
	spec.FromTree(ctx, map[string]any{}, &empty, &diags)
	if !empty.Interfaces.IsNull() {
		t.Fatal("no interfaces should read as null")
	}
}

func TestPrefixImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{
		"0.0.0.0/0": true, "198.51.100.0/24": true, "192.0.2.1/32": true,
		"192.0.2.1/24": false, "2001:db8::/32": false, "192.0.2.0": false, "": false,
	} {
		if _, err := prefixImport(id); (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

// Acceptance tests against the fake router.

const routeTwoHops = `
resource "edgeos_static_route" "office" {
  destination = "198.51.100.0/24"
  next_hops = {
    "192.168.1.254" = {
      description = "primary"
    }
    "192.168.2.254" = {
      distance = 10
    }
  }
}
`

const routeUpdated = `
resource "edgeos_static_route" "office" {
  destination = "198.51.100.0/24"
  next_hops = {
    "192.168.1.254" = {
      description = "primary link"
      distance    = 5
    }
  }
  blackhole = {
    distance = 250
  }
}
`

func TestAccStaticRoute(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("protocols static route 198.51.100.0/24", "protocols static route 203.0.113.0/24"),
		Steps: []resource.TestStep{
			{
				Config: e.config(routeTwoHops),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_static_route.office", "id", "198.51.100.0/24"),
					resource.TestCheckResourceAttr("edgeos_static_route.office", "next_hops.192.168.2.254.disabled", "false"),
					resource.TestCheckNoResourceAttr("edgeos_static_route.office", "blackhole"),
					e.checkValue("protocols static route 198.51.100.0/24", map[string]any{
						"next-hop": map[string]any{
							"192.168.1.254": map[string]any{"description": "primary"},
							"192.168.2.254": map[string]any{"distance": "10"},
						},
					}),
				),
			},
			{
				// Update in place: drop a gateway, change one and add a blackhole.
				// A route Terraform does not manage survives.
				PreConfig: e.set("protocols static route 192.0.2.0/24 next-hop 192.168.1.1", configtree.Flag()),
				Config:    e.config(routeUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_route.office", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("protocols static route 198.51.100.0/24", map[string]any{
						"next-hop": map[string]any{
							"192.168.1.254": map[string]any{"description": "primary link", "distance": "5"},
						},
						"blackhole": map[string]any{"distance": "250"},
					}),
					e.checkValue("protocols static route 192.0.2.0/24 next-hop", map[string]any{"192.168.1.1": nil}),
				),
			},
			{
				ResourceName:      "edgeos_static_route.office",
				ImportState:       true,
				ImportStateId:     "198.51.100.0/24",
				ImportStateVerify: true,
			},
			{
				// Drift: a gateway added and the blackhole disabled by hand.
				PreConfig: func() {
					e.set("protocols static route 198.51.100.0/24 next-hop 192.168.9.9", configtree.Flag())()
					e.set("protocols static route 198.51.100.0/24 blackhole disable", configtree.Flag())()
				},
				Config: e.config(routeUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_route.office", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("protocols static route 198.51.100.0/24 next-hop 192.168.9.9"),
					e.checkValue("protocols static route 198.51.100.0/24 blackhole", map[string]any{"distance": "250"}),
				),
			},
			{
				// Drift: the route is deleted by hand and recreated.
				PreConfig: e.del("protocols static route 198.51.100.0/24"),
				Config:    e.config(routeUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_route.office", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("protocols static route 198.51.100.0/24 next-hop 192.168.1.254 distance", "5"),
			},
			{
				// A new destination replaces the route.
				Config: e.config(`
resource "edgeos_static_route" "office" {
  destination = "203.0.113.0/24"
  blackhole   = {}
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_route.office", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("protocols static route 198.51.100.0/24"),
					e.checkValue("protocols static route 203.0.113.0/24", map[string]any{"blackhole": nil}),
				),
			},
		},
	})
}

func TestAccStaticInterfaceRoute(t *testing.T) {
	e := newAccEnv(t)
	const cfg = `
resource "edgeos_static_interface_route" "vpn" {
  destination = "10.66.0.0/24"
  next_hop_interfaces = {
    wg0 = {
      description = "site to site"
    }
  }
}
`
	const updated = `
resource "edgeos_static_interface_route" "vpn" {
  destination = "10.66.0.0/24"
  next_hop_interfaces = {
    wg0 = {
      description = "site to site"
    }
    wg1 = {
      distance = 20
      disabled = true
    }
  }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("protocols static interface-route 10.66.0.0/24", "protocols static interface-route 10.67.0.0/24"),
		Steps: []resource.TestStep{
			{
				Config: e.config(cfg),
				Check: e.checkValue("protocols static interface-route 10.66.0.0/24", map[string]any{
					"next-hop-interface": map[string]any{"wg0": map[string]any{"description": "site to site"}},
				}),
			},
			{
				Config: e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_interface_route.vpn", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("protocols static interface-route 10.66.0.0/24 next-hop-interface wg1", map[string]any{"distance": "20", "disable": nil}),
			},
			{
				ResourceName:      "edgeos_static_interface_route.vpn",
				ImportState:       true,
				ImportStateId:     "10.66.0.0/24",
				ImportStateVerify: true,
			},
			{
				PreConfig: e.set("protocols static interface-route 10.66.0.0/24 next-hop-interface wg1 distance", configtree.Single("30")),
				Config:    e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_interface_route.vpn", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("protocols static interface-route 10.66.0.0/24 next-hop-interface wg1 distance", "20"),
			},
			{
				PreConfig: e.del("protocols static interface-route 10.66.0.0/24"),
				Config:    e.config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_interface_route.vpn", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("protocols static interface-route 10.66.0.0/24 next-hop-interface wg0 description", "site to site"),
			},
			{
				Config: e.config(`
resource "edgeos_static_interface_route" "vpn" {
  destination         = "10.67.0.0/24"
  next_hop_interfaces = { wg0 = {} }
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_static_interface_route.vpn", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("protocols static interface-route 10.66.0.0/24"),
					e.checkValue("protocols static interface-route 10.67.0.0/24 next-hop-interface", map[string]any{"wg0": nil}),
				),
			},
		},
	})
}

func TestAccStaticRouteErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("zebra: route install failed") },
				Config:      e.config(routeTwoHops),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*zebra: route install failed`),
			},
			{
				PreConfig:   e.set("protocols static route 198.51.100.0/24 blackhole", configtree.Flag()),
				Config:      e.config(routeTwoHops),
				ExpectError: regexp.MustCompile(`(?s)already exists.*Import\s+it`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("protocols static route 198.51.100.0/24 next-hop")); ok {
		t.Fatal("the failed commit must leave the router unchanged")
	}
}

func TestAccStaticRouteValidation(t *testing.T) {
	e := newAccEnv(t)
	cases := []struct {
		config string
		want   string
	}{
		{`resource "edgeos_static_route" "r" {
  destination = "198.51.100.0/24"
}`, `No next hop`},
		{`resource "edgeos_static_route" "r" {
  destination = "198.51.100.1/24"
  blackhole   = {}
}`, `host bits set`},
		{`resource "edgeos_static_route" "r" {
  destination = "2001:db8::/32"
  blackhole   = {}
}`, `not IPv4`},
		{`resource "edgeos_static_route" "r" {
  destination = "198.51.100.0/24"
  next_hops   = {}
}`, `at least 1`},
		{`resource "edgeos_static_route" "r" {
  destination = "198.51.100.0/24"
  next_hops   = { "gateway" = {} }
}`, `not an IPv4 address`},
		{`resource "edgeos_static_route" "r" {
  destination = "198.51.100.0/24"
  next_hops   = { "192.168.1.1" = { distance = 0 } }
}`, `between 1 and 255`},
		{`resource "edgeos_static_route" "r" {
  destination = "198.51.100.0/24"
  blackhole   = { description = "say \"no\"" }
}`, `double quote`},
		{`resource "edgeos_static_interface_route" "r" {
  destination         = "10.66.0.0/24"
  next_hop_interfaces = { "WG 0" = {} }
}`, `must be an interface name`},
		{`resource "edgeos_static_interface_route" "r" {
  destination = "10.66.0.0/24"
}`, `next_hop_interfaces`},
	}
	for _, tc := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config(tc.config),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
