// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Synthetic keys: 32 bytes of 0x01 and 0xfb, which cover '/', '+' and '='.
const (
	testKey  = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	testKey2 = "+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/s="
)

func TestValidateSetDHCPWireguard(t *testing.T) {
	t.Parallel()
	sn := "service dhcp-server shared-network-name LAN subnet 192.168.1.0/24 "
	cases := []struct {
		path string
		val  configtree.Value
		want string // substring of the error, "" for accepted
	}{
		{sn + "default-router", configtree.Single("192.168.1.1"), ""},
		{sn + "default-router", configtree.Single("gw.example"), "not a valid IPv4 address"},
		{sn + "dns-server", configtree.Multi("192.168.1.1", "2001:db8::1"), "not a valid IPv4 address"},
		{sn + "start 192.168.1.10 stop", configtree.Single("192.168.1.20"), ""},
		{sn + "start 192.168.1.x stop", configtree.Single("192.168.1.20"), "not a valid IPv4 address"},
		{sn + "static-mapping p ip-address", configtree.Single("192.168.1.300"), "not a valid IPv4 address"},
		{sn + "lease", configtree.Single("60"), "at least 120"},
		{"service dhcp-server shared-network-name LAN subnet 192.168.1.1/24 lease", configtree.Single("600"), "not a valid IPv4 network"},
		{"service dhcp-server disabled", configtree.Single("maybe"), "not one of"},
		{"service dhcp-server use-dnsmasq", configtree.Single("enable"), ""},
		{"interfaces wireguard wg0 private-key", configtree.Single(testKey), ""},
		{"interfaces wireguard wg0 private-key", configtree.Single("/config/auth/wg0.key"), ""},
		{"interfaces wireguard wg0 private-key", configtree.Single("secret"), "base64 encoded key"},
		{"interfaces wireguard wg0 fwmark", configtree.Single("-1"), "fwmark"},
		{"interfaces wireguard wg0 address", configtree.Multi("10.8.0.1"), "prefix length"},
		{"interfaces wireguard wg0 peer " + testKey2 + " allowed-ips", configtree.Multi("10.8.0.2/32"), ""},
		{"interfaces wireguard wg0 peer " + testKey2 + " allowed-ips", configtree.Multi("10.8.0.2"), "not a valid IP prefix"},
		{"interfaces wireguard wg0 peer " + testKey2 + " endpoint", configtree.Single("[2001:db8::1]:51820"), ""},
		{"interfaces wireguard wg0 peer " + testKey2 + " endpoint", configtree.Single("vpn.example.com"), "host:port"},
		{"interfaces wireguard wg0 peer " + testKey2 + " endpoint", configtree.Single(":51820"), "host:port"},
		{"interfaces wireguard wg0 peer " + testKey2 + " preshared-key", configtree.Single("x"), "base64 encoded key"},
		{"interfaces wireguard wg0 peer not-a-key allowed-ips", configtree.Multi("10.8.0.2/32"), "not a valid WireGuard public key"},
	}
	for _, tc := range cases {
		got := validateSet(cliPath(t, tc.path), tc.val)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s = %v: got %q, want %q", tc.path, tc.val, got, tc.want)
		}
	}
	if !isMultiNode(cliPath(t, sn+"wins-server")) || !isMultiNode(cliPath(t, sn+"time-server")) {
		t.Error("wins-server and time-server are multi-valued")
	}
}

func TestValidateCommitDHCP(t *testing.T) {
	t.Parallel()
	tree := configtree.Normalize(map[string]any{"service": map[string]any{"dhcp-server": map[string]any{
		"shared-network-name": map[string]any{"LAN": map[string]any{"subnet": map[string]any{
			"192.168.1.0/24": map[string]any{
				"start": map[string]any{
					"192.168.1.10":  map[string]any{"stop": "192.168.1.20"},
					"192.168.1.50":  map[string]any{"stop": "192.168.1.40"},
					"192.168.2.10":  map[string]any{"stop": "192.168.2.20"},
					"192.168.1.100": nil,
				},
				"static-mapping": map[string]any{
					"ok":  map[string]any{"ip-address": "192.168.1.5", "mac-address": "02:00:5e:00:53:05"},
					"bad": map[string]any{"ip-address": "10.0.0.5", "mac-address": "02:00:5e:00:53:06"},
				},
			},
			"garbage": map[string]any{"lease": "600"},
		}}},
	}}}).(map[string]any)
	var msgs []string
	for _, is := range checkDHCPCommit(tree) {
		msgs = append(msgs, is.Path+": "+is.Msg)
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{
		"start 192.168.1.50: DHCP range 192.168.1.50-192.168.1.40 is not inside",
		"start 192.168.2.10: DHCP range",
		"start 192.168.1.100: stop must be set",
		"static-mapping bad: Static mapping address 10.0.0.5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(msgs) != 4 {
		t.Errorf("got %d issues:\n%s", len(msgs), got)
	}
}

func cliPath(t *testing.T, s string) configtree.Path {
	t.Helper()
	p, err := configtree.ParsePath(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
