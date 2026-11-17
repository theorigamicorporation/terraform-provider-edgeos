// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// testWGKey returns a throwaway X25519 key pair in WireGuard's base64 form.
func testWGKey(t *testing.T) (priv, pub string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}

// testWGKeyWith returns a public key that contains all of '/', '+' and '=',
// which must survive paths, IDs and imports.
func testWGKeyWith(t *testing.T) string {
	t.Helper()
	for {
		_, pub := testWGKey(t)
		if strings.ContainsAny(pub, "/") && strings.Contains(pub, "+") {
			return pub
		}
	}
}

// Unit tests.

func TestWireguardKeyHelpers(t *testing.T) {
	t.Parallel()
	priv, pub := testWGKey(t)
	if got, ok := wgPublicKey(priv); !ok || got != pub {
		t.Errorf("wgPublicKey = %q, %v; want %q", got, ok, pub)
	}
	// RFC 7748 section 6.1 test vector (Alice).
	alice, _ := base64.StdEncoding.DecodeString("dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=")
	if got, _ := wgPublicKey(base64.StdEncoding.EncodeToString(alice)); got != "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo=" {
		t.Errorf("RFC 7748 vector: got %q", got)
	}
	for s, want := range map[string]bool{
		priv: true, pub: true,
		"":                            false,
		priv[:43]:                     false,
		strings.Repeat("A", 44):       false, // 33 bytes, not padded
		strings.Repeat("!", 43) + "=": false,
	} {
		if isWGKey(s) != want {
			t.Errorf("isWGKey(%q) = %v", s, !want)
		}
	}
	for s, want := range map[string]bool{
		"/config/auth/wg0.key": true, "/config/auth/wg0-laptop.psk": true, "/k": true,
		"config/auth/wg0.key": false, "/config/../etc/shadow": false, "/config/./x": false,
		"/config/auth/my key": false, "/config//x": false, "/": false,
	} {
		if isKeyFile(s) != want {
			t.Errorf("isKeyFile(%q) = %v", s, !want)
		}
	}
	if _, ok := wgPublicKey("/config/auth/wg0.key"); ok {
		t.Error("a file path has no public key")
	}
	if !publicKeyOf(types.StringUnknown()).IsUnknown() || !publicKeyOf(types.StringNull()).IsNull() {
		t.Error("publicKeyOf must pass unknown through and map null to null")
	}
}

func TestSecretFromDevice(t *testing.T) {
	t.Parallel()
	priv, _ := testWGKey(t)
	other, _ := testWGKey(t)
	prior := types.StringValue(priv)
	cases := []struct {
		name   string
		device types.String
		want   types.String
	}{
		{"same", types.StringValue(priv), prior},
		{"changed key", types.StringValue(other), types.StringValue(other)},
		{"changed to a file", types.StringValue("/config/auth/wg0.key"), types.StringValue("/config/auth/wg0.key")},
		{"missing", types.StringNull(), prior},
		{"placeholder", types.StringValue("********"), prior},
		{"empty", types.StringValue(""), prior},
	}
	for _, tc := range cases {
		if got := secretFromDevice(tc.device, prior); !got.Equal(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCheckEndpoint(t *testing.T) {
	t.Parallel()
	for s, ok := range map[string]bool{
		"vpn.example.com:51820": true, "203.0.113.10:51820": true, "[2001:db8::1]:51820": true,
		"host:1": true, "a-b.example.:65535": true,
		"vpn.example.com": false, "203.0.113.10:0": false, "203.0.113.10:65536": false,
		"2001:db8::1:51820": false, "[vpn.example.com]:51820": false, "[fe80::1%eth0]:51820": false,
		"-bad.example.com:1": false, "under_score.example:1": false, ":51820": false, "host:port": false,
	} {
		if err := checkEndpoint(s); (err == nil) != ok {
			t.Errorf("%q: err = %v", s, err)
		}
	}
}

func TestWireguardInterfaceMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	priv, pub := testWGKey(t)
	null := wireguardInterfaceModel{
		Addresses: types.SetNull(types.StringType), Description: types.StringNull(), Disabled: types.BoolValue(false),
		ListenPort: types.Int64Null(), MTU: types.Int64Null(), PrivateKey: types.StringValue("/config/auth/wg0.key"),
		RouteAllowedIPs: types.BoolNull(), FWMark: types.Int64Null(),
		FirewallIn: types.StringNull(), FirewallOut: types.StringNull(), FirewallLocal: types.StringNull(),
	}
	full := wireguardInterfaceModel{
		Addresses: stringSet([]string{"fd00:8::1/64", "10.8.0.1/24"}), Description: types.StringValue("road warrior"),
		Disabled: types.BoolValue(true), ListenPort: types.Int64Value(51820), MTU: types.Int64Value(1420),
		PrivateKey: types.StringValue(priv), RouteAllowedIPs: types.BoolValue(false), FWMark: types.Int64Value(51),
		FirewallIn: types.StringValue("WG_IN"), FirewallOut: types.StringValue("WG_OUT"), FirewallLocal: types.StringValue("WG_LOCAL"),
	}
	cases := []struct {
		name   string
		model  wireguardInterfaceModel
		leaves map[string]configtree.Value
		pub    types.String
	}{
		{"file key", null, map[string]configtree.Value{"private-key": configtree.Single("/config/auth/wg0.key")}, types.StringNull()},
		{"everything", full, map[string]configtree.Value{
			"address":             configtree.UnorderedMulti("10.8.0.1/24", "fd00:8::1/64"),
			"description":         configtree.Single("road warrior"),
			"disable":             configtree.Flag(),
			"listen-port":         configtree.Single("51820"),
			"mtu":                 configtree.Single("1420"),
			"private-key":         configtree.Single(priv),
			"route-allowed-ips":   configtree.Single("false"),
			"fwmark":              configtree.Single("51"),
			"firewall in name":    configtree.Single("WG_IN"),
			"firewall out name":   configtree.Single("WG_OUT"),
			"firewall local name": configtree.Single("WG_LOCAL"),
		}, types.StringValue(pub)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := wireguardInterfaceToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)
			back := wireguardInterfaceModel{}
			wireguardInterfaceFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, wireguardInterfaceToLeaves(ctx, &back, &diags), tc.leaves)
			if !back.PublicKey.Equal(tc.pub) {
				t.Errorf("public key = %v, want %v", back.PublicKey, tc.pub)
			}
		})
	}

	// A hidden key keeps the configured one.
	var diags diag.Diagnostics
	m := wireguardInterfaceModel{PrivateKey: types.StringValue(priv)}
	wireguardInterfaceFromTree(ctx, map[string]any{"private-key": "********", "mtu": "big"}, &m, &diags)
	if m.PrivateKey.ValueString() != priv || m.PublicKey.ValueString() != pub {
		t.Errorf("hidden key: private %v public %v", m.PrivateKey, m.PublicKey)
	}
	if !diags.HasError() {
		t.Error("a non-numeric mtu should be reported")
	}
}

func TestWireguardPeerMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	psk, _ := testWGKey(t)
	cases := []struct {
		name   string
		model  wireguardPeerModel
		leaves map[string]configtree.Value
	}{
		{"minimal", wireguardPeerModel{
			AllowedIPs: stringSet([]string{"10.8.0.2/32"}), Description: types.StringNull(), Disabled: types.BoolValue(false),
			Endpoint: types.StringNull(), PersistentKeepalive: types.Int64Null(), PresharedKey: types.StringNull(),
		}, map[string]configtree.Value{"allowed-ips": configtree.UnorderedMulti("10.8.0.2/32")}},
		{"everything", wireguardPeerModel{
			AllowedIPs: stringSet([]string{"192.168.50.0/24", "10.8.0.3/32", "fd00:8::3/128"}), Description: types.StringValue("site b"),
			Disabled: types.BoolValue(true), Endpoint: types.StringValue("[2001:db8::3]:51820"),
			PersistentKeepalive: types.Int64Value(25), PresharedKey: types.StringValue(psk),
		}, map[string]configtree.Value{
			"allowed-ips":          configtree.UnorderedMulti("10.8.0.3/32", "192.168.50.0/24", "fd00:8::3/128"),
			"description":          configtree.Single("site b"),
			"disable":              configtree.Flag(),
			"endpoint":             configtree.Single("[2001:db8::3]:51820"),
			"persistent-keepalive": configtree.Single("25"),
			"preshared-key":        configtree.Single(psk),
		}},
		{"psk file", wireguardPeerModel{
			AllowedIPs: stringSet([]string{"0.0.0.0/0"}), Disabled: types.BoolValue(false),
			PresharedKey: types.StringValue("/config/auth/wg0-laptop.psk"),
		}, map[string]configtree.Value{
			"allowed-ips":   configtree.UnorderedMulti("0.0.0.0/0"),
			"preshared-key": configtree.Single("/config/auth/wg0-laptop.psk"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := wireguardPeerToLeaves(ctx, &tc.model, &diags)
			assertLeaves(t, l, tc.leaves)
			back := wireguardPeerModel{}
			wireguardPeerFromTree(ctx, configtree.Build(l), &back, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, wireguardPeerToLeaves(ctx, &back, &diags), tc.leaves)
		})
	}

	// A hidden preshared key keeps the prior value; a missing one is gone.
	var diags diag.Diagnostics
	m := wireguardPeerModel{PresharedKey: types.StringValue(psk)}
	wireguardPeerFromTree(ctx, map[string]any{"allowed-ips": []string{"10.8.0.2/32"}, "preshared-key": "<hidden>"}, &m, &diags)
	if m.PresharedKey.ValueString() != psk {
		t.Errorf("hidden psk = %v", m.PresharedKey)
	}
	wireguardPeerFromTree(ctx, map[string]any{"allowed-ips": []string{"10.8.0.2/32"}}, &m, &diags)
	if !m.PresharedKey.IsNull() {
		t.Errorf("missing psk = %v", m.PresharedKey)
	}
}

func TestWireguardPeerImport(t *testing.T) {
	t.Parallel()
	pub := testWGKeyWith(t)
	for id, ok := range map[string]bool{
		"wg0:" + pub:        true,
		"wg999:" + pub:      true,
		"wg0":               false,
		"wg0:":              false,
		"eth0:" + pub:       false,
		"wg0:" + pub[:40]:   false,
		"wg0:" + pub + ":x": false,
		":" + pub:           false,
	} {
		attrs, err := wireguardPeerImport(id)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
		if ok && attrs["public_key"].(types.String).ValueString() != pub {
			t.Errorf("%q: attrs = %v", id, attrs)
		}
	}
}

// Acceptance tests against the fake router.

func TestAccWireguardInterface(t *testing.T) {
	e := newAccEnv(t)
	priv, pub := testWGKey(t)
	priv2, pub2 := testWGKey(t)
	peerKey := testWGKeyWith(t)
	iface := func(key, extra string) string {
		if !strings.Contains(extra, "addresses") {
			extra = `addresses = ["10.8.0.1/24"]
  ` + extra
		}
		return e.config(fmt.Sprintf(`
resource "edgeos_firewall_name" "wg_in" {
  name = "WG_IN"
}

resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  listen_port = 51820
  private_key = %q
  %s
}
`, key, extra))
	}
	base := "interfaces wireguard wg0"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed(base, "interfaces wireguard wg1"),
		Steps: []resource.TestStep{
			{
				Config: iface(priv, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectSensitiveValue("edgeos_wireguard_interface.wg0", tfjsonpath.New("private_key")),
					// The public key is known at plan time.
					plancheck.ExpectKnownValue("edgeos_wireguard_interface.wg0", tfjsonpath.New("public_key"), knownvalue.StringExact(pub)),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_wireguard_interface.wg0", "id", "wg0"),
					resource.TestCheckResourceAttr("edgeos_wireguard_interface.wg0", "public_key", pub),
					e.checkValue(base, map[string]any{
						"address": []string{"10.8.0.1/24"}, "listen-port": "51820", "private-key": priv,
					}),
				),
			},
			{
				// Update in place leaves peers alone, including ones nobody manages.
				PreConfig: e.set(base+" peer "+peerKey+" allowed-ips", configtree.Multi("10.8.0.9/32")),
				Config: iface(priv2, `description = "road warrior"
  addresses         = ["10.8.0.1/24", "fd00:8::1/64"]
  mtu               = 1380
  route_allowed_ips = false
  fwmark            = 51
  disabled          = true
  firewall_in       = edgeos_firewall_name.wg_in.name`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue("edgeos_wireguard_interface.wg0", tfjsonpath.New("public_key"), knownvalue.StringExact(pub2)),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue(base, map[string]any{
						"address": []string{"10.8.0.1/24", "fd00:8::1/64"}, "description": "road warrior",
						"listen-port": "51820", "mtu": "1380", "private-key": priv2, "route-allowed-ips": "false",
						"fwmark": "51", "disable": nil, "firewall": map[string]any{"in": map[string]any{"name": "WG_IN"}},
						"peer": map[string]any{peerKey: map[string]any{"allowed-ips": []string{"10.8.0.9/32"}}},
					}),
					func(*terraform.State) error {
						if b := toJSON(e.srv.LastBatch()); strings.Contains(b, "peer") {
							t.Errorf("update touched peers: %s", b)
						}
						return nil
					},
				),
			},
			{
				// Switching to a key file: no public key any more.
				Config: iface("/config/auth/wg0.key", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue("edgeos_wireguard_interface.wg0", tfjsonpath.New("public_key"), knownvalue.Null()),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("edgeos_wireguard_interface.wg0", "public_key"),
					e.checkValue(base+" private-key", "/config/auth/wg0.key"),
					e.checkAbsent(base+" mtu"),
					e.checkAbsent(base+" firewall"),
					e.checkValue(base+" peer "+peerKey+" allowed-ips", []string{"10.8.0.9/32"}),
				),
			},
			{
				ResourceName:      "edgeos_wireguard_interface.wg0",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// The router hides the key from the session: no diff.
				PreConfig: e.set(base+" private-key", configtree.Single("********")),
				Config:    iface("/config/auth/wg0.key", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
				Check: resource.TestCheckResourceAttr("edgeos_wireguard_interface.wg0", "private_key", "/config/auth/wg0.key"),
			},
			{
				// Drift: listen port and key changed by hand are set back.
				PreConfig: func() {
					e.set(base+" private-key", configtree.Single(priv))()
					e.set(base+" listen-port", configtree.Single("51999"))()
				},
				Config: iface("/config/auth/wg0.key", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue(base+" private-key", "/config/auth/wg0.key"),
					e.checkValue(base+" listen-port", "51820"),
				),
			},
			{
				// Drift: deleted by hand and recreated.
				PreConfig: e.del(base),
				Config:    iface("/config/auth/wg0.key", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue(base+" listen-port", "51820"),
			},
			{
				// Renaming replaces the interface.
				Config: e.config(`
resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg1"
  private_key = "/config/auth/wg1.key"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(base),
					e.checkValue("interfaces wireguard wg1", map[string]any{"private-key": "/config/auth/wg1.key"}),
				),
			},
		},
	})
}

func TestAccWireguardPeer(t *testing.T) {
	e := newAccEnv(t)
	pub := testWGKeyWith(t)
	pub2, _ := testWGKey(t)
	psk, _ := testWGKey(t)
	peer := func(key, extra string) string {
		if !strings.Contains(extra, "allowed_ips") {
			extra = `allowed_ips = ["10.8.0.2/32"]
  ` + extra
		}
		return e.config(fmt.Sprintf(`
resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  addresses   = ["10.8.0.1/24"]
  listen_port = 51820
  private_key = "/config/auth/wg0.key"
}

resource "edgeos_wireguard_peer" "laptop" {
  interface   = edgeos_wireguard_interface.wg0.name
  public_key  = %q
  %s
}
`, key, extra))
	}
	peerPath := "interfaces wireguard wg0 peer " + pub
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("interfaces wireguard wg0"),
		Steps: []resource.TestStep{
			{
				Config: peer(pub, `description = "laptop"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_wireguard_peer.laptop", "id", "wg0:"+pub),
					resource.TestCheckResourceAttr("edgeos_wireguard_peer.laptop", "disabled", "false"),
					e.checkValue(peerPath, map[string]any{"allowed-ips": []string{"10.8.0.2/32"}, "description": "laptop"}),
				),
			},
			{
				// Update in place with an inline preshared key that stays out of the plan.
				Config: peer(pub, fmt.Sprintf(`allowed_ips = ["10.8.0.2/32", "fd00:8::2/128"]
  endpoint             = "vpn.example.com:51820"
  persistent_keepalive = 25
  disabled             = true
  preshared_key        = %q`, psk)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_peer.laptop", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionNoop),
					plancheck.ExpectSensitiveValue("edgeos_wireguard_peer.laptop", tfjsonpath.New("preshared_key")),
				}},
				Check: e.checkValue(peerPath, map[string]any{
					"allowed-ips": []string{"10.8.0.2/32", "fd00:8::2/128"}, "endpoint": "vpn.example.com:51820",
					"persistent-keepalive": "25", "disable": nil, "preshared-key": psk,
				}),
			},
			{
				ResourceName:      "edgeos_wireguard_peer.laptop",
				ImportState:       true,
				ImportStateId:     "wg0:" + pub,
				ImportStateVerify: true,
			},
			{
				// Drift: allowed IPs widened by hand are narrowed again.
				PreConfig: e.set(peerPath+" allowed-ips", configtree.Multi("0.0.0.0/0")),
				Config: peer(pub, `allowed_ips = ["10.8.0.2/32", "fd00:8::2/128"]
  preshared_key = "/config/auth/wg0-laptop.psk"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_peer.laptop", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue(peerPath, map[string]any{
						"allowed-ips": []string{"10.8.0.2/32", "fd00:8::2/128"}, "preshared-key": "/config/auth/wg0-laptop.psk",
					}),
				),
			},
			{
				// Drift: deleted by hand and recreated.
				PreConfig: e.del(peerPath),
				Config:    peer(pub, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_peer.laptop", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue(peerPath, map[string]any{"allowed-ips": []string{"10.8.0.2/32"}}),
			},
			{
				// A new public key replaces the peer.
				Config: peer(pub2, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_peer.laptop", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(peerPath),
					e.checkValue("interfaces wireguard wg0 peer "+pub2+" allowed-ips", []string{"10.8.0.2/32"}),
				),
			},
			{
				// Updating the interface keeps the managed peer.
				Config: e.config(fmt.Sprintf(`
resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  addresses   = ["10.8.0.1/24"]
  listen_port = 51821
  private_key = "/config/auth/wg0.key"
}

resource "edgeos_wireguard_peer" "laptop" {
  interface   = edgeos_wireguard_interface.wg0.name
  public_key  = %q
  allowed_ips = ["10.8.0.2/32"]
}
`, pub2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_wireguard_interface.wg0", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_wireguard_peer.laptop", plancheck.ResourceActionNoop),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("edgeos_wireguard_peer.laptop", tfjsonpath.New("id"), knownvalue.StringExact("wg0:"+pub2)),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("interfaces wireguard wg0 listen-port", "51821"),
					e.checkValue("interfaces wireguard wg0 peer "+pub2+" allowed-ips", []string{"10.8.0.2/32"}),
				),
			},
		},
	})
}

func TestAccWireguardValidation(t *testing.T) {
	e := newAccEnv(t)
	priv, pub := testWGKey(t)
	iface := func(body string) string {
		return `
resource "edgeos_wireguard_interface" "x" {
  name = "wg0"
  ` + body + `
}`
	}
	peer := func(body string) string {
		return `
resource "edgeos_wireguard_peer" "x" {
  interface = "wg0"
  ` + body + `
}`
	}
	cases := []struct {
		body string
		want string
	}{
		{iface(`private_key = "not-a-key"`), `Invalid WireGuard key`},
		{iface(`private_key = "/config/auth/../wg0.key"`), `Invalid key file path`},
		{iface(`private_key = "/config/auth/my key"`), `Invalid key file path`},
		{iface(`private_key = "` + priv[:43] + `"`), `Invalid WireGuard key`},
		{`
resource "edgeos_wireguard_interface" "x" {
  name        = "wg1000"
  private_key = "/config/auth/wg.key"
}`, `wg0 to wg999`},
		{iface(`private_key = "/config/auth/wg0.key"
  mtu = 60`), `between 68 and 9000`},
		{iface(`private_key = "/config/auth/wg0.key"
  listen_port = 70000`), `between 1 and 65535`},
		{iface(`private_key = "/config/auth/wg0.key"
  addresses = ["10.8.0.1"]`), `with prefix length`},
		{iface(`private_key = "/config/auth/wg0.key"
  firewall_in = "bad name"`), `1 to 27 characters`},
		{peer(`public_key = "` + pub + `"
  allowed_ips = []`), `at least 1`},
		{peer(`public_key = "/config/auth/peer.pub"
  allowed_ips = ["10.8.0.2/32"]`), `Invalid WireGuard key`},
		{peer(`public_key = "` + pub + `"
  allowed_ips = ["10.8.0.2/32"]
  endpoint = "2001:db8::1:51820"`), `Invalid endpoint`},
		{peer(`public_key = "` + pub + `"
  allowed_ips = ["10.8.0.2/32"]
  endpoint = "vpn.example.com"`), `Invalid endpoint`},
		{peer(`public_key = "` + pub + `"
  allowed_ips = ["10.8.0.2/32"]
  persistent_keepalive = 0`), `between 1 and 65535`},
		{peer(`public_key = "` + pub + `"
  allowed_ips = ["10.8.0.2/32"]
  preshared_key = "short"`), `Invalid WireGuard key`},
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

func TestAccWireguardErrors(t *testing.T) {
	e := newAccEnv(t)
	pub := testWGKeyWith(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("interfaces wireguard wg0", "interfaces wireguard wg5"),
		Steps: []resource.TestStep{
			{
				// Commit failure: nothing is created and the error is shown.
				PreConfig: func() { e.srv.FailNextCommit("wireguard: kernel module not loaded") },
				Config: e.config(`
resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  private_key = "/config/auth/wg0.key"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*kernel module not loaded`),
				Check:       e.checkAbsent("interfaces wireguard wg0"),
			},
			{
				// A peer on an interface that does not exist fails at commit.
				Config: e.config(fmt.Sprintf(`
resource "edgeos_wireguard_peer" "orphan" {
  interface   = "wg5"
  public_key  = %q
  allowed_ips = ["10.8.0.2/32"]
}`, pub)),
				ExpectError: regexp.MustCompile(`private-key must be set`),
			},
			{
				// A failed peer commit leaves the router unchanged.
				PreConfig: func() { e.srv.FailNextCommit("wg: setconf failed") },
				Config: e.config(fmt.Sprintf(`
resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  private_key = "/config/auth/wg0.key"
}

resource "edgeos_wireguard_peer" "p" {
  interface   = edgeos_wireguard_interface.wg0.name
  public_key  = %q
  allowed_ips = ["10.8.0.2/32"]
}`, pub)),
				ExpectError: regexp.MustCompile(`wg: setconf failed`),
				Check:       e.checkAbsent("interfaces wireguard wg0"),
			},
		},
	})
}
