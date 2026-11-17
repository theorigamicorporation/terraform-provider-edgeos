// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Unit tests for the model mapping.

func toJSON(x any) string {
	b, _ := json.Marshal(x)
	return string(b)
}

func nullSubnet() dhcpSubnetModel {
	nl := types.ListNull(types.StringType)
	return dhcpSubnetModel{
		DefaultRouter: types.StringNull(), DNSServers: nl, DomainName: types.StringNull(), Lease: types.Int64Null(),
		NTPServers: nl, TimeServers: nl, WINSServers: nl, Ranges: types.SetNull(dhcpRangeType),
		Exclude: types.SetNull(types.StringType), TFTPServerName: types.StringNull(), BootfileName: types.StringNull(),
		UnifiController: types.StringNull(), SubnetParameters: nl,
	}
}

func strList(vals ...string) types.List {
	elems := make([]attr.Value, 0, len(vals))
	for _, v := range vals {
		elems = append(elems, types.StringValue(v))
	}
	return types.ListValueMust(types.StringType, elems)
}

func rangeSet(t *testing.T, pairs ...string) types.Set {
	t.Helper()
	var rs []dhcpRangeModel
	for i := 0; i+1 < len(pairs); i += 2 {
		rs = append(rs, dhcpRangeModel{Start: types.StringValue(pairs[i]), Stop: types.StringValue(pairs[i+1])})
	}
	v, d := types.SetValueFrom(context.Background(), dhcpRangeType, rs)
	if d.HasError() {
		t.Fatal(d)
	}
	return v
}

func subnetMap(t *testing.T, subnets map[string]dhcpSubnetModel) types.Map {
	t.Helper()
	v, d := types.MapValueFrom(context.Background(), dhcpSubnetType, subnets)
	if d.HasError() {
		t.Fatal(d)
	}
	return v
}

func TestDHCPSharedNetworkMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	full := nullSubnet()
	full.DefaultRouter = types.StringValue("192.168.1.1")
	full.DNSServers = strList("192.168.1.1", "198.51.100.53")
	full.DomainName = types.StringValue("home.arpa")
	full.Lease = types.Int64Value(86400)
	full.NTPServers = strList("192.168.1.1")
	full.TimeServers = strList("192.168.1.2")
	full.WINSServers = strList("192.168.1.3")
	full.Ranges = rangeSet(t, "192.168.1.100", "192.168.1.149", "192.168.1.200", "192.168.1.249")
	full.Exclude = stringSet([]string{"192.168.1.120", "192.168.1.110"})
	full.TFTPServerName = types.StringValue("192.168.1.5")
	full.BootfileName = types.StringValue("pxelinux.0")
	full.UnifiController = types.StringValue("192.168.1.6")
	full.SubnetParameters = strList("option domain-search &quot;home.arpa&quot;;")

	empty := nullSubnet()
	nl := types.ListNull(types.StringType)

	cases := []struct {
		name   string
		model  dhcpSharedNetworkModel
		leaves map[string]configtree.Value
	}{
		{
			name: "empty subnet",
			model: dhcpSharedNetworkModel{
				Disabled: types.BoolValue(false), Authoritative: types.BoolNull(), Parameters: nl,
				Description: types.StringNull(),
				Subnets:     subnetMap(t, map[string]dhcpSubnetModel{"192.0.2.0/24": empty}),
			},
			leaves: map[string]configtree.Value{"subnet 192.0.2.0/24": configtree.Flag()},
		},
		{
			name: "everything",
			model: dhcpSharedNetworkModel{
				Description:   types.StringValue("main LAN"),
				Authoritative: types.BoolValue(true),
				Disabled:      types.BoolValue(true),
				Parameters:    strList("ping-check true;"),
				Subnets: subnetMap(t, map[string]dhcpSubnetModel{
					"192.168.1.0/24": full,
					"192.168.2.0/24": empty,
				}),
			},
			leaves: map[string]configtree.Value{
				"description":                                    configtree.Single("main LAN"),
				"authoritative":                                  configtree.Single("enable"),
				"disable":                                        configtree.Flag(),
				"shared-network-parameters":                      configtree.Multi("ping-check true;"),
				"subnet 192.168.1.0/24":                          configtree.Flag(),
				"subnet 192.168.1.0/24 default-router":           configtree.Single("192.168.1.1"),
				"subnet 192.168.1.0/24 dns-server":               configtree.Multi("192.168.1.1", "198.51.100.53"),
				"subnet 192.168.1.0/24 domain-name":              configtree.Single("home.arpa"),
				"subnet 192.168.1.0/24 lease":                    configtree.Single("86400"),
				"subnet 192.168.1.0/24 ntp-server":               configtree.Multi("192.168.1.1"),
				"subnet 192.168.1.0/24 time-server":              configtree.Multi("192.168.1.2"),
				"subnet 192.168.1.0/24 wins-server":              configtree.Multi("192.168.1.3"),
				"subnet 192.168.1.0/24 start 192.168.1.100":      configtree.Flag(),
				"subnet 192.168.1.0/24 start 192.168.1.100 stop": configtree.Single("192.168.1.149"),
				"subnet 192.168.1.0/24 start 192.168.1.200":      configtree.Flag(),
				"subnet 192.168.1.0/24 start 192.168.1.200 stop": configtree.Single("192.168.1.249"),
				"subnet 192.168.1.0/24 exclude":                  configtree.UnorderedMulti("192.168.1.110", "192.168.1.120"),
				"subnet 192.168.1.0/24 tftp-server-name":         configtree.Single("192.168.1.5"),
				"subnet 192.168.1.0/24 bootfile-name":            configtree.Single("pxelinux.0"),
				"subnet 192.168.1.0/24 unifi-controller":         configtree.Single("192.168.1.6"),
				"subnet 192.168.1.0/24 subnet-parameters":        configtree.Multi("option domain-search &quot;home.arpa&quot;;"),
				"subnet 192.168.2.0/24":                          configtree.Flag(),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := dhcpSharedNetworkToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)

			// Round trip through the tree the router would store. The
			// prior subnets tell FromTree which empty subnets it owns.
			back := dhcpSharedNetworkModel{Subnets: tc.model.Subnets}
			dhcpSharedNetworkFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, dhcpSharedNetworkToLeaves(ctx, &back, &diags), tc.leaves)
			if !back.Subnets.Equal(tc.model.Subnets) {
				t.Errorf("subnets = %v, want %v", back.Subnets, tc.model.Subnets)
			}
		})
	}
}

func TestDHCPSharedNetworkFromTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tree := map[string]any{
		"authoritative": "disable",
		"subnet": map[string]any{
			// Only static mappings: owned by edgeos_dhcp_static_mapping.
			"192.0.2.0/24": map[string]any{"static-mapping": map[string]any{
				"x": map[string]any{"ip-address": "192.0.2.10", "mac-address": "02:00:5e:00:53:10"},
			}},
			// A pool without stop reads as a null stop.
			"198.51.100.0/24": map[string]any{"start": map[string]any{"198.51.100.10": nil}, "lease": "nope"},
		},
	}
	var diags diag.Diagnostics
	m := dhcpSharedNetworkModel{Subnets: types.MapNull(dhcpSubnetType)}
	dhcpSharedNetworkFromTree(ctx, tree, &m, &diags)
	if !diags.HasError() {
		t.Error("a non-numeric lease should be reported")
	}
	if m.Authoritative.ValueBool() || m.Authoritative.IsNull() {
		t.Errorf("authoritative = %v", m.Authoritative)
	}
	subs := m.Subnets.Elements()
	if _, ok := subs["192.0.2.0/24"]; ok || len(subs) != 1 {
		t.Fatalf("subnets = %v", m.Subnets)
	}
	s := knownSubnets(ctx, m.Subnets, &diags)["198.51.100.0/24"]
	rs := knownRanges(ctx, s.Ranges, &diags)
	if len(rs) != 1 || rs[0].Start.ValueString() != "198.51.100.10" || !rs[0].Stop.IsNull() {
		t.Errorf("ranges = %v", s.Ranges)
	}

	// No subnets at all reads as null, and an empty start node as no pools.
	m = dhcpSharedNetworkModel{Subnets: types.MapNull(dhcpSubnetType)}
	dhcpSharedNetworkFromTree(ctx, map[string]any{"description": "x"}, &m, &diags)
	if !m.Subnets.IsNull() {
		t.Errorf("subnets = %v", m.Subnets)
	}
	if !dhcpRangesFromTree(map[string]any{"start": map[string]any{}}).IsNull() {
		t.Error("empty start node should read as null")
	}

	// Unknown subnets and ranges are skipped while validating.
	if got := knownSubnets(ctx, types.MapUnknown(dhcpSubnetType), &diags); len(got) != 0 {
		t.Errorf("unknown map gave %v", got)
	}
	withUnknown := types.MapValueMust(dhcpSubnetType, map[string]attr.Value{"192.0.2.0/24": types.ObjectUnknown(dhcpSubnetType.AttrTypes)})
	if got := knownSubnets(ctx, withUnknown, &diags); len(got) != 0 {
		t.Errorf("unknown element gave %v", got)
	}
	unknownRange := types.SetValueMust(dhcpRangeType, []attr.Value{types.ObjectUnknown(dhcpRangeAttrTypes)})
	if got := knownRanges(ctx, unknownRange, &diags); len(got) != 0 {
		t.Errorf("unknown range gave %v", got)
	}
}

func TestDHCPStaticMappingMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := newDHCPStaticMappingResource()().(*leafResource[dhcpStaticMappingModel]).spec
	cases := []struct {
		name   string
		model  dhcpStaticMappingModel
		leaves map[string]configtree.Value
	}{
		{
			name: "minimal",
			model: dhcpStaticMappingModel{
				IPAddress: types.StringValue("192.168.1.20"), MACAddress: types.StringValue("02:00:5e:00:53:20"),
				Disabled: types.BoolValue(false), Parameters: types.ListNull(types.StringType),
			},
			leaves: map[string]configtree.Value{
				"ip-address":  configtree.Single("192.168.1.20"),
				"mac-address": configtree.Single("02:00:5e:00:53:20"),
			},
		},
		{
			name: "disabled with parameters",
			model: dhcpStaticMappingModel{
				IPAddress: types.StringValue("192.168.1.21"), MACAddress: types.StringValue("02:00:5e:00:53:21"),
				Disabled: types.BoolValue(true), Parameters: strList("option host-name printer;"),
			},
			leaves: map[string]configtree.Value{
				"ip-address":                configtree.Single("192.168.1.21"),
				"mac-address":               configtree.Single("02:00:5e:00:53:21"),
				"disable":                   configtree.Flag(),
				"static-mapping-parameters": configtree.Multi("option host-name printer;"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := spec.ToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := dhcpStaticMappingModel{}
			spec.FromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}

	// An upper case MAC set on the router reads as lower case.
	var diags diag.Diagnostics
	m := dhcpStaticMappingModel{}
	spec.FromTree(ctx, map[string]any{"ip-address": "192.0.2.1", "mac-address": "02:00:5E:00:53:AA"}, &m, &diags)
	if m.MACAddress.ValueString() != "02:00:5e:00:53:aa" {
		t.Errorf("mac = %v", m.MACAddress)
	}
}

func TestDHCPStaticMappingImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{
		"LAN:192.168.1.0/24:printer":  true,
		"lan_2.x:10.0.0.0/8:a-b.c_d":  true,
		"LAN:192.168.1.0/24":          false,
		"LAN:192.168.1.0/24:a:b":      false,
		"bad name:192.168.1.0/24:x":   false,
		"LAN:192.168.1.1/24:printer":  false,
		"LAN:2001:db8::/32:printer":   false,
		"LAN:192.168.1.0/24:bad name": false,
		"LAN:not-a-subnet:printer":    false,
		"::":                          false,
	} {
		attrs, err := dhcpStaticMappingImport(id)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
		if ok && attrs["subnet"].(types.String).ValueString() == "" {
			t.Errorf("%q: attrs = %v", id, attrs)
		}
	}
}

// Acceptance tests against the fake router.

const lanNetwork = `
resource "edgeos_dhcp_shared_network" "lan" {
  name          = "LAN"
  authoritative = true
  subnets = {
    "192.168.1.0/24" = {
      default_router = "192.168.1.1"
      dns_servers    = ["192.168.1.1"]
      domain_name    = "home.arpa"
      lease          = 86400
      ranges         = [{ start = "192.168.1.100", stop = "192.168.1.199" }]
    }
  }
}
`

func TestAccDHCPSharedNetwork(t *testing.T) {
	e := newAccEnv(t)
	const updated = `
resource "edgeos_dhcp_shared_network" "lan" {
  name        = "LAN"
  description = "main LAN"
  subnets = {
    "192.168.1.0/24" = {
      default_router = "192.168.1.1"
      dns_servers    = ["192.168.1.1", "198.51.100.53"]
      domain_name    = "home.arpa"
      lease          = 3600
      ranges = [
        { start = "192.168.1.100", stop = "192.168.1.149" },
        { start = "192.168.1.200", stop = "192.168.1.249" },
      ]
      exclude = ["192.168.1.120"]
    }
    "192.168.2.0/24" = {
      default_router = "192.168.2.1"
      ranges         = [{ start = "192.168.2.10", stop = "192.168.2.50" }]
    }
  }
}
`
	const mapping = `
resource "edgeos_dhcp_static_mapping" "printer" {
  shared_network = edgeos_dhcp_shared_network.lan.name
  subnet         = "192.168.1.0/24"
  name           = "printer"
  ip_address     = "192.168.1.20"
  mac_address    = "02:00:5e:00:53:20"
}
`
	base := "service dhcp-server shared-network-name LAN"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed(base, "service dhcp-server shared-network-name GUEST"),
		Steps: []resource.TestStep{
			{
				Config: e.config(lanNetwork + mapping),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_dhcp_shared_network.lan", "id", "LAN"),
					resource.TestCheckResourceAttr("edgeos_dhcp_shared_network.lan", "disabled", "false"),
					resource.TestCheckResourceAttr("edgeos_dhcp_static_mapping.printer", "id", "LAN:192.168.1.0/24:printer"),
					e.checkValue(base, map[string]any{
						"authoritative": "enable",
						"subnet": map[string]any{"192.168.1.0/24": map[string]any{
							"default-router": "192.168.1.1",
							"dns-server":     []string{"192.168.1.1"},
							"domain-name":    "home.arpa",
							"lease":          "86400",
							"start":          map[string]any{"192.168.1.100": map[string]any{"stop": "192.168.1.199"}},
							"static-mapping": map[string]any{"printer": map[string]any{
								"ip-address": "192.168.1.20", "mac-address": "02:00:5e:00:53:20",
							}},
						}},
					}),
				),
			},
			{
				// Update in place: a static mapping nobody manages and
				// unmodelled subnet options must survive.
				PreConfig: func() {
					e.set(base+" subnet 192.168.1.0/24 static-mapping manual ip-address", configtree.Single("192.168.1.30"))()
					e.set(base+" subnet 192.168.1.0/24 static-mapping manual mac-address", configtree.Single("02:00:5e:00:53:30"))()
					e.set(base+" subnet 192.168.1.0/24 static-route destination-subnet", configtree.Single("203.0.113.0/24"))()
				},
				Config: e.config(updated + mapping),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_shared_network.lan", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_dhcp_static_mapping.printer", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(base+" authoritative"),
					e.checkValue(base+" description", "main LAN"),
					e.checkValue(base+" subnet 192.168.1.0/24 dns-server", []string{"192.168.1.1", "198.51.100.53"}),
					e.checkValue(base+" subnet 192.168.1.0/24 lease", "3600"),
					e.checkValue(base+" subnet 192.168.1.0/24 start", map[string]any{
						"192.168.1.100": map[string]any{"stop": "192.168.1.149"},
						"192.168.1.200": map[string]any{"stop": "192.168.1.249"},
					}),
					e.checkValue(base+" subnet 192.168.1.0/24 exclude", []string{"192.168.1.120"}),
					e.checkValue(base+" subnet 192.168.1.0/24 static-mapping printer ip-address", "192.168.1.20"),
					e.checkValue(base+" subnet 192.168.1.0/24 static-mapping manual ip-address", "192.168.1.30"),
					e.checkValue(base+" subnet 192.168.1.0/24 static-route destination-subnet", "203.0.113.0/24"),
					e.checkValue(base+" subnet 192.168.2.0/24", map[string]any{
						"default-router": "192.168.2.1",
						"start":          map[string]any{"192.168.2.10": map[string]any{"stop": "192.168.2.50"}},
					}),
					func(*terraform.State) error {
						// The update must not mention static mappings at all.
						if b := e.srv.LastBatch(); regexp.MustCompile(`static-mapping`).MatchString(toJSON(b)) {
							t.Errorf("update touched static mappings: %s", toJSON(b))
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "edgeos_dhcp_shared_network.lan",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Drift: lease changed, a pool and a subnet added by hand.
				PreConfig: func() {
					e.set(base+" subnet 192.168.1.0/24 lease", configtree.Single("600"))()
					e.set(base+" subnet 192.168.1.0/24 start 192.168.1.50 stop", configtree.Single("192.168.1.60"))()
					e.set(base+" subnet 192.168.3.0/24 default-router", configtree.Single("192.168.3.1"))()
				},
				Config: e.config(updated + mapping),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_shared_network.lan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue(base+" subnet 192.168.1.0/24 lease", "3600"),
					e.checkAbsent(base+" subnet 192.168.1.0/24 start 192.168.1.50"),
					e.checkAbsent(base+" subnet 192.168.3.0/24"),
				),
			},
			{
				// Removing a subnet deletes it; the remaining one keeps its mappings.
				Config: e.config(lanNetwork + mapping),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_shared_network.lan", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(base+" subnet 192.168.2.0/24"),
					e.checkAbsent(base+" description"),
					e.checkAbsent(base+" subnet 192.168.1.0/24 exclude"),
					e.checkValue(base+" authoritative", "enable"),
					e.checkValue(base+" subnet 192.168.1.0/24 static-mapping printer mac-address", "02:00:5e:00:53:20"),
					e.checkValue(base+" subnet 192.168.1.0/24 static-mapping manual mac-address", "02:00:5e:00:53:30"),
				),
			},
			{
				// Drift: the shared network is deleted out of band and recreated.
				PreConfig: e.del(base),
				Config:    e.config(lanNetwork),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_shared_network.lan", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue(base+" subnet 192.168.1.0/24 default-router", "192.168.1.1"),
			},
			{
				// Renaming replaces the shared network.
				Config: e.config(`
resource "edgeos_dhcp_shared_network" "lan" {
  name = "GUEST"
  subnets = {
    "192.168.9.0/24" = {}
  }
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_shared_network.lan", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(base),
					e.checkValue("service dhcp-server shared-network-name GUEST subnet 192.168.9.0/24", nil),
				),
			},
		},
	})
}

func TestAccDHCPStaticMapping(t *testing.T) {
	e := newAccEnv(t)
	mapping := func(name, ip, mac, extra string) string {
		return e.config(lanNetwork + `
resource "edgeos_dhcp_static_mapping" "printer" {
  shared_network = edgeos_dhcp_shared_network.lan.name
  subnet         = "192.168.1.0/24"
  name           = "` + name + `"
  ip_address     = "` + ip + `"
  mac_address    = "` + mac + `"
  ` + extra + `
}`)
	}
	base := "service dhcp-server shared-network-name LAN subnet 192.168.1.0/24 static-mapping"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("service dhcp-server shared-network-name LAN"),
		Steps: []resource.TestStep{
			{
				Config: mapping("printer", "192.168.1.20", "02:00:5e:00:53:20", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_dhcp_static_mapping.printer", "disabled", "false"),
					e.checkValue(base+" printer", map[string]any{"ip-address": "192.168.1.20", "mac-address": "02:00:5e:00:53:20"}),
				),
			},
			{
				// Update in place leaves the shared network alone.
				Config: mapping("printer", "192.168.1.21", "02:00:5e:00:53:21", `disabled = true
  static_mapping_parameters = ["option host-name printer;"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_static_mapping.printer", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_dhcp_shared_network.lan", plancheck.ResourceActionNoop),
				}},
				Check: e.checkValue(base+" printer", map[string]any{
					"ip-address": "192.168.1.21", "mac-address": "02:00:5e:00:53:21", "disable": nil,
					"static-mapping-parameters": []string{"option host-name printer;"},
				}),
			},
			{
				ResourceName:      "edgeos_dhcp_static_mapping.printer",
				ImportState:       true,
				ImportStateId:     "LAN:192.168.1.0/24:printer",
				ImportStateVerify: true,
			},
			{
				// Drift: the MAC is changed by hand and set back.
				PreConfig: e.set(base+" printer mac-address", configtree.Single("02:00:5e:00:53:99")),
				Config: mapping("printer", "192.168.1.21", "02:00:5e:00:53:21", `disabled = true
  static_mapping_parameters = ["option host-name printer;"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_static_mapping.printer", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue(base+" printer mac-address", "02:00:5e:00:53:21"),
			},
			{
				// Drift: deleted by hand and recreated.
				PreConfig: e.del(base + " printer"),
				Config:    mapping("printer", "192.168.1.21", "02:00:5e:00:53:21", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_static_mapping.printer", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue(base+" printer", map[string]any{"ip-address": "192.168.1.21", "mac-address": "02:00:5e:00:53:21"}),
			},
			{
				// Renaming replaces the mapping.
				Config: mapping("label-printer", "192.168.1.21", "02:00:5e:00:53:21", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_dhcp_static_mapping.printer", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(base+" printer"),
					e.checkValue(base+" label-printer ip-address", "192.168.1.21"),
					resource.TestCheckResourceAttr("edgeos_dhcp_static_mapping.printer", "id", "LAN:192.168.1.0/24:label-printer"),
				),
			},
		},
	})
}

func TestAccDHCPValidation(t *testing.T) {
	e := newAccEnv(t)
	network := func(subnets string) string {
		return `
resource "edgeos_dhcp_shared_network" "n" {
  name    = "LAN"
  subnets = ` + subnets + `
}`
	}
	cases := []struct {
		body string
		want string
	}{
		{network(`{ "192.168.1.0/24" = { ranges = [{ start = "192.168.2.1", stop = "192.168.2.9" }] } }`), `not inside 192.168.1.0/24`},
		{network(`{ "192.168.1.0/24" = { ranges = [{ start = "192.168.1.50", stop = "192.168.1.10" }] } }`), `lower than start`},
		{network(`{ "192.168.1.0/24" = { ranges = [
  { start = "192.168.1.10", stop = "192.168.1.50" },
  { start = "192.168.1.50", stop = "192.168.1.60" },
] } }`), `Overlapping DHCP ranges`},
		{network(`{ "192.168.1.0/24" = { ranges = [
  { start = "192.168.1.10", stop = "192.168.1.50" },
  { start = "192.168.1.10", stop = "192.168.1.60" },
] } }`), `Duplicate range start`},
		{network(`{ "192.168.1.1/24" = {} }`), `host bits set`},
		{network(`{ "2001:db8::/64" = {} }`), `not IPv4`},
		{network(`{}`), `at least 1`},
		{network(`{ "192.168.1.0/24" = { lease = 60 } }`), `between 120 and`},
		{network(`{ "192.168.1.0/24" = { dns_servers = ["dns.example"] } }`), `not an IPv4 address`},
		{network(`{ "192.168.1.0/24" = { dns_servers = [] } }`), `at least 1`},
		{network(`{ "192.168.1.0/24" = { subnet_parameters = ["option x \"y\";"] } }`), `double quote`},
		{`
resource "edgeos_dhcp_shared_network" "n" {
  name    = "bad name"
  subnets = { "192.168.1.0/24" = {} }
}`, `must only use letters`},
		{`
resource "edgeos_dhcp_static_mapping" "m" {
  shared_network = "LAN"
  subnet         = "192.168.1.0/24"
  name           = "x"
  ip_address     = "192.168.2.20"
  mac_address    = "02:00:5e:00:53:20"
}`, `192.168.2.20 is not inside 192.168.1.0/24`},
		{`
resource "edgeos_dhcp_static_mapping" "m" {
  shared_network = "LAN"
  subnet         = "192.168.1.0/24"
  name           = "x"
  ip_address     = "192.168.1.20"
  mac_address    = "02:00:5E:00:53:20"
}`, `lower case MAC`},
	}
	for _, tc := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config(tc.body),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}

func TestAccDHCPWarningAndErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("service dhcp-server shared-network-name LAN"),
		Steps: []resource.TestStep{
			{
				// Commit failure: nothing is created and the error is shown.
				PreConfig:   func() { e.srv.FailNextCommit("dhcpd: subnet declaration not found") },
				Config:      e.config(lanNetwork),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*subnet declaration not found`),
				Check:       e.checkAbsent("service dhcp-server shared-network-name LAN"),
			},
			{
				// A gateway outside the subnet is only a warning.
				Config: e.config(`
resource "edgeos_dhcp_shared_network" "lan" {
  name    = "LAN"
  subnets = { "192.168.1.0/24" = { default_router = "192.168.7.1" } }
}`),
				Check: e.checkValue("service dhcp-server shared-network-name LAN subnet 192.168.1.0/24 default-router", "192.168.7.1"),
			},
			{
				// A failed update keeps the old state.
				PreConfig:   func() { e.srv.FailNextCommit("dhcpd: failed") },
				Config:      e.config(lanNetwork),
				ExpectError: regexp.MustCompile(`EdgeOS rejected the change`),
				Check:       e.checkValue("service dhcp-server shared-network-name LAN subnet 192.168.1.0/24 default-router", "192.168.7.1"),
			},
			{
				// The fake rejects a mapping outside its subnet at commit, like EdgeOS.
				PreConfig:   e.set("service dhcp-server shared-network-name LAN subnet 192.168.1.0/24 static-mapping x ip-address", configtree.Single("10.0.0.1")),
				Config:      e.config(lanNetwork),
				ExpectError: regexp.MustCompile(`not inside subnet`),
			},
		},
	})
}
