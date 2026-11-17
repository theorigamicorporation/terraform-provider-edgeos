// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func natBase(typ string) natRuleModel {
	return natRuleModel{
		Type: types.StringValue(typ), Disabled: types.BoolValue(false), Exclude: types.BoolValue(false),
		Description: types.StringNull(), InboundInterface: types.StringNull(), OutboundInterface: types.StringNull(),
		Protocol: types.StringNull(), Log: types.BoolNull(), Number: types.Int64Value(5000),
	}
}

func TestNATRuleMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	masq := natBase("masquerade")
	masq.OutboundInterface = types.StringValue("eth0")

	dnat := natBase("destination")
	dnat.Description = types.StringValue("web server")
	dnat.InboundInterface = types.StringValue("eth0")
	dnat.Protocol = types.StringValue("tcp")
	dnat.Log = types.BoolValue(true)
	dnat.Destination = &fwDest{
		Address: types.StringValue("203.0.113.2"), Port: types.StringValue("443"),
		AddressGroup: types.StringNull(), NetworkGroup: types.StringNull(), PortGroup: types.StringNull(),
	}
	dnat.InsideAddress = &natAddress{Address: types.StringValue("192.168.1.20"), Port: types.StringValue("8443")}

	snat := natBase("source")
	snat.OutboundInterface = types.StringValue("eth0.10")
	snat.Disabled = types.BoolValue(true)
	snat.Exclude = types.BoolValue(true)
	snat.Log = types.BoolValue(false)
	snat.Source = &fwDest{
		Address: types.StringNull(), Port: types.StringNull(), AddressGroup: types.StringValue("SERVERS"),
		NetworkGroup: types.StringValue("LANS"), PortGroup: types.StringValue("WEB"),
	}
	snat.OutsideAddress = &natAddress{Address: types.StringValue("203.0.113.8/29"), Port: types.StringNull()}

	cases := []struct {
		name   string
		model  natRuleModel
		leaves map[string]configtree.Value
	}{
		{
			name:  "masquerade",
			model: masq,
			leaves: map[string]configtree.Value{
				"type":               configtree.Single("masquerade"),
				"outbound-interface": configtree.Single("eth0"),
			},
		},
		{
			name:  "destination with ports",
			model: dnat,
			leaves: map[string]configtree.Value{
				"type":                   configtree.Single("destination"),
				"description":            configtree.Single("web server"),
				"inbound-interface":      configtree.Single("eth0"),
				"protocol":               configtree.Single("tcp"),
				"log":                    configtree.Single("enable"),
				"destination address":    configtree.Single("203.0.113.2"),
				"destination port":       configtree.Single("443"),
				"inside-address address": configtree.Single("192.168.1.20"),
				"inside-address port":    configtree.Single("8443"),
			},
		},
		{
			name:  "source with groups",
			model: snat,
			leaves: map[string]configtree.Value{
				"type":                       configtree.Single("source"),
				"outbound-interface":         configtree.Single("eth0.10"),
				"disable":                    configtree.Flag(),
				"exclude":                    configtree.Flag(),
				"log":                        configtree.Single("disable"),
				"source group address-group": configtree.Single("SERVERS"),
				"source group network-group": configtree.Single("LANS"),
				"source group port-group":    configtree.Single("WEB"),
				"outside-address address":    configtree.Single("203.0.113.8/29"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := natRuleToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)

			back := natRuleModel{}
			natRuleFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, natRuleToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}
}

func TestNATRuleFromTreeIgnoresEmptyContainers(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	tree := map[string]any{"type": "masquerade", "source": map[string]any{}, "outside-address": map[string]any{}}
	m := natRuleModel{}
	natRuleFromTree(context.Background(), tree, &m, &diags)
	if m.Source != nil || m.OutsideAddress != nil || m.Destination != nil || m.InsideAddress != nil {
		t.Fatalf("empty containers became objects: %+v", m)
	}
}

func TestNATRuleImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{
		"1": true, "9999": true, "5010": true, "0": false, "10000": false, "x": false, "": false, "010": false, "-1": false,
	} {
		attrs, err := natRuleImport(id)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
		if ok && attrs["number"].String() != id {
			t.Errorf("%q: number = %v", id, attrs["number"])
		}
	}
}

func TestValidateNATRule(t *testing.T) {
	t.Parallel()
	addr := &natAddress{Address: types.StringValue("192.168.1.20"), Port: types.StringNull()}
	cases := []struct {
		name     string
		mod      func(m *natRuleModel)
		errors   []string
		warnings []string
	}{
		{"masquerade ok", func(m *natRuleModel) { m.OutboundInterface = types.StringValue("eth0") }, nil, nil},
		{"masquerade low number", func(m *natRuleModel) {
			m.Number = types.Int64Value(100)
			m.OutboundInterface = types.StringValue("eth0")
		}, nil, []string{"Source rule outside 5000-9999"}},
		{"destination high number", func(m *natRuleModel) {
			m.Type = types.StringValue("destination")
			m.InboundInterface = types.StringValue("eth0")
			m.InsideAddress = addr
		}, nil, []string{"Destination rule outside 1-4999"}},
		{"destination missing everything", func(m *natRuleModel) {
			m.Number = types.Int64Value(10)
			m.Type = types.StringValue("destination")
			m.OutboundInterface = types.StringValue("eth1")
			m.OutsideAddress = addr
		}, []string{"Missing inbound_interface", "Missing inside_address", "outbound_interface not allowed", "outside_address not allowed"}, nil},
		{"destination exclude", func(m *natRuleModel) {
			m.Number = types.Int64Value(10)
			m.Type = types.StringValue("destination")
			m.InboundInterface = types.StringValue("eth0")
			m.Exclude = types.BoolValue(true)
		}, nil, nil},
		{"source missing outside", func(m *natRuleModel) {
			m.Type = types.StringValue("source")
			m.InboundInterface = types.StringValue("eth1")
			m.InsideAddress = addr
		}, []string{"Missing outbound_interface", "inbound_interface not allowed", "inside_address not allowed", "Missing outside_address"}, nil},
		{"source exclude", func(m *natRuleModel) {
			m.Type = types.StringValue("source")
			m.OutboundInterface = types.StringValue("eth0")
			m.Exclude = types.BoolValue(true)
		}, nil, nil},
		{"masquerade with outside", func(m *natRuleModel) {
			m.OutboundInterface = types.StringValue("eth0")
			m.OutsideAddress = addr
		}, []string{"outside_address not allowed"}, nil},
		{"ports without protocol", func(m *natRuleModel) {
			m.Type = types.StringValue("source")
			m.OutboundInterface = types.StringValue("eth0")
			m.OutsideAddress = &natAddress{Address: types.StringValue("203.0.113.1"), Port: types.StringValue("1024-2048")}
			m.Source = &fwDest{Address: types.StringNull(), Port: types.StringValue("53"), AddressGroup: types.StringNull(), NetworkGroup: types.StringNull(), PortGroup: types.StringNull()}
			m.Destination = &fwDest{Address: types.StringNull(), Port: types.StringValue("53"), AddressGroup: types.StringNull(), NetworkGroup: types.StringNull(), PortGroup: types.StringNull()}
			m.Protocol = types.StringValue("icmp")
		}, []string{"Port needs tcp or udp", "Port needs tcp or udp", "Port needs tcp or udp"}, nil},
		{"inside port without protocol", func(m *natRuleModel) {
			m.Number = types.Int64Value(10)
			m.Type = types.StringValue("destination")
			m.InboundInterface = types.StringValue("eth0")
			m.InsideAddress = &natAddress{Address: types.StringValue("192.168.1.2"), Port: types.StringValue("22")}
		}, []string{"Port needs tcp or udp"}, nil},
		{"ports with negated tcp_udp", func(m *natRuleModel) {
			m.OutboundInterface = types.StringValue("eth0")
			m.Protocol = types.StringValue("!tcp_udp")
			m.Destination = &fwDest{Address: types.StringNull(), Port: types.StringValue("53"), AddressGroup: types.StringNull(), NetworkGroup: types.StringNull(), PortGroup: types.StringNull()}
		}, nil, nil},
		{"empty and conflicting match", func(m *natRuleModel) {
			m.OutboundInterface = types.StringValue("eth0")
			m.Source = &fwDest{Address: types.StringNull(), Port: types.StringNull(), AddressGroup: types.StringNull(), NetworkGroup: types.StringNull(), PortGroup: types.StringNull()}
			m.Destination = &fwDest{Address: types.StringValue("192.0.2.1"), Port: types.StringNull(), AddressGroup: types.StringValue("X"), NetworkGroup: types.StringNull(), PortGroup: types.StringNull()}
		}, []string{"Empty source block", "Conflicting destination match"}, nil},
		{"unknown type skips type checks", func(m *natRuleModel) { m.Type = types.StringUnknown() }, nil, nil},
		{"unknown interface counts as set", func(m *natRuleModel) { m.OutboundInterface = types.StringUnknown() }, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := natBase("masquerade")
			tc.mod(&m)
			var diags diag.Diagnostics
			validateNATRule(context.Background(), &m, &diags)
			assertDiags(t, diags.Errors(), tc.errors)
			assertDiags(t, diags.Warnings(), tc.warnings)
		})
	}
}

func assertDiags(t *testing.T, got diag.Diagnostics, want []string) {
	t.Helper()
	var summaries []string
	for _, d := range got {
		summaries = append(summaries, d.Summary())
	}
	if len(summaries) != len(want) {
		t.Fatalf("got %q, want %q", summaries, want)
	}
	for _, w := range want {
		found := false
		for _, s := range summaries {
			if s == w {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q in %q", w, summaries)
		}
	}
}

func TestTitleCase(t *testing.T) {
	t.Parallel()
	if titleCase("") != "" || titleCase("source") != "Source" {
		t.Fatal("titleCase")
	}
}

// Acceptance tests against the fake router.

const natDNAT = `
resource "edgeos_nat_rule" "web" {
  number            = 10
  type              = "destination"
  description       = "HTTPS to the web server"
  inbound_interface = "eth0"
  protocol          = "tcp"
  destination = {
    address = "203.0.113.2"
    port    = "443"
  }
  inside_address = {
    address = "192.168.1.20"
  }
}
`

func natMasq(number int, extra string) string {
	return `
resource "edgeos_nat_rule" "masq" {
  number             = ` + strconv.Itoa(number) + `
  type               = "masquerade"
  outbound_interface = "eth0"
  ` + extra + `
}
`
}

func TestAccNATRule(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("service nat rule 10", "service nat rule 5000", "service nat rule 5010"),
		Steps: []resource.TestStep{
			{
				Config: e.config(natDNAT + natMasq(5000, `description = "LAN to WAN"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_nat_rule.web", "id", "10"),
					resource.TestCheckResourceAttr("edgeos_nat_rule.web", "disabled", "false"),
					resource.TestCheckResourceAttr("edgeos_nat_rule.web", "exclude", "false"),
					resource.TestCheckNoResourceAttr("edgeos_nat_rule.web", "source"),
					e.checkValue("service nat rule 10", map[string]any{
						"type": "destination", "description": "HTTPS to the web server", "inbound-interface": "eth0",
						"protocol":       "tcp",
						"destination":    map[string]any{"address": "203.0.113.2", "port": "443"},
						"inside-address": map[string]any{"address": "192.168.1.20"},
					}),
					e.checkValue("service nat rule 5000", map[string]any{
						"type": "masquerade", "outbound-interface": "eth0", "description": "LAN to WAN",
					}),
				),
			},
			{
				// Update in place: change the inside port, log and disable the masquerade rule.
				PreConfig: func() {
					e.set("service nat rule 20 type", configtree.Single("masquerade"))()
					e.set("service nat rule 20 outbound-interface", configtree.Single("eth1"))()
				},
				Config: e.config(strings.Replace(natDNAT, `address = "192.168.1.20"`, `address = "192.168.1.20"
    port    = "8443"`, 1) + natMasq(5000, `log = true
  disabled = true
  source = { address = "192.168.1.0/24" }`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_nat_rule.web", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_nat_rule.masq", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service nat rule 10 inside-address", map[string]any{"address": "192.168.1.20", "port": "8443"}),
					e.checkValue("service nat rule 5000", map[string]any{
						"type": "masquerade", "outbound-interface": "eth0", "log": "enable", "disable": nil,
						"source": map[string]any{"address": "192.168.1.0/24"},
					}),
					// A rule Terraform does not manage survives.
					e.checkValue("service nat rule 20 type", "masquerade"),
				),
			},
			{
				ResourceName:      "edgeos_nat_rule.web",
				ImportState:       true,
				ImportStateId:     "10",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "edgeos_nat_rule.masq",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Drift: the inside address is changed and a protocol added out of band.
				PreConfig: func() {
					e.set("service nat rule 10 inside-address address", configtree.Single("192.168.1.99"))()
					e.set("service nat rule 5000 protocol", configtree.Single("udp"))()
				},
				Config: e.config(strings.Replace(natDNAT, `address = "192.168.1.20"`, `address = "192.168.1.20"
    port    = "8443"`, 1) + natMasq(5000, `log = true
  disabled = true
  source = { address = "192.168.1.0/24" }`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_nat_rule.web", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_nat_rule.masq", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service nat rule 10 inside-address address", "192.168.1.20"),
					e.checkAbsent("service nat rule 5000 protocol"),
				),
			},
			{
				// Drift: the rule is deleted out of band and gets recreated.
				PreConfig: e.del("service nat rule 10"),
				Config: e.config(natDNAT + natMasq(5000, `log = true
  disabled = true
  source = { address = "192.168.1.0/24" }`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_nat_rule.web", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("service nat rule 10 inside-address", map[string]any{"address": "192.168.1.20"}),
			},
			{
				// Changing the number replaces the rule; changing the type does not.
				Config: e.config(natDNAT + `
resource "edgeos_nat_rule" "masq" {
  number             = 5010
  type               = "source"
  outbound_interface = "eth0"
  outside_address = {
    address = "203.0.113.8/29"
  }
}
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_nat_rule.masq", plancheck.ResourceActionDestroyBeforeCreate),
					plancheck.ExpectResourceAction("edgeos_nat_rule.web", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("service nat rule 5000"),
					e.checkValue("service nat rule 5010", map[string]any{
						"type": "source", "outbound-interface": "eth0",
						"outside-address": map[string]any{"address": "203.0.113.8/29"},
					}),
				),
			},
		},
	})
}

func TestAccNATRuleErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("iptables-restore: line 7 failed") },
				Config:      e.config(natMasq(5000, "")),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*iptables-restore: line 7 failed`),
			},
			{
				PreConfig:   e.set("service nat rule 5000 type", configtree.Single("masquerade")),
				Config:      e.config(natMasq(5000, "")),
				ExpectError: regexp.MustCompile(`(?s)already exists.*Import\s+it`),
			},
		},
	})
	if _, ok := e.srv.Get(cliPath("service nat rule 5000 outbound-interface")); ok {
		t.Fatal("the failed create must not reach the router")
	}
}

func TestAccNATRuleValidation(t *testing.T) {
	e := newAccEnv(t)
	cases := []struct {
		body string
		want string
	}{
		{`type = "destination"
  number = 10`, `Missing inbound_interface`},
		{`type = "destination"
  number = 10
  inbound_interface = "eth0"`, `Missing inside_address`},
		{`type = "source"
  outbound_interface = "eth0"`, `Missing outside_address`},
		{`type = "masquerade"`, `Missing outbound_interface`},
		{`type = "masquerade"
  outbound_interface = "eth0"
  outside_address = { address = "203.0.113.1" }`, `outside_address not allowed`},
		{`type = "masquerade"
  outbound_interface = "eth0"
  destination = { port = "53" }`, `Port needs tcp or udp`},
		{`type = "masquerade"
  outbound_interface = "eth0"
  protocol = "icmp"
  source = { port = "53" }`, `Port needs tcp or udp`},
		{`type = "hairpin"`, `value must be one of`},
		{`type = "masquerade"
  number = 0
  outbound_interface = "eth0"`, `number value must be between 1 and 9999`},
		{`type = "masquerade"
  outbound_interface = "eth 0"`, `must be an interface name`},
		{`type = "masquerade"
  outbound_interface = "eth0"
  description = "say \"hi\""`, `double quote`},
	}
	for _, tc := range cases {
		body := tc.body
		if !strings.Contains(body, "number") {
			body += "\n  number = 5000"
		}
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config: e.config(`
resource "edgeos_nat_rule" "r" {
  ` + body + `
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
