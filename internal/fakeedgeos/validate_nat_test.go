// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func mustPath(t *testing.T, s string) configtree.Path {
	t.Helper()
	p, err := configtree.ParsePath(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNATSetChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		value configtree.Value
		want  string
	}{
		{"protocols static route 198.51.100.0/24 next-hop 192.0.2.1", configtree.Flag(), ""},
		{"protocols static route 198.51.100.1/24 blackhole", configtree.Flag(), "Invalid network"},
		{"protocols static route 2001:db8::/32 blackhole", configtree.Flag(), "not a valid IPv4 prefix"},
		{"protocols static route 198.51.100.0/24 next-hop gw", configtree.Flag(), "not a valid IPv4 address"},
		{"protocols static route 198.51.100.0/24 next-hop 192.0.2.1 distance", configtree.Single("0"), "Must be between 1-255"},
		{"protocols static interface-route 10.0.0.0/8 next-hop-interface wg0 distance", configtree.Single("300"), "Must be between 1-255"},
		{"protocols static interface-route 10.0.0.1/8 next-hop-interface wg0", configtree.Flag(), "Invalid network"},
		{"port-forward rule 0 original-port", configtree.Single("22"), "positive 32 bit"},
		{"port-forward rule 4294967296 original-port", configtree.Single("22"), "positive 32 bit"},
		{"port-forward rule 1 forward-to address", configtree.Single("192.168.1.300"), "not a valid IPv4 address"},
		{"port-forward rule 1 forward-to address", configtree.Single("192.168.1.30"), ""},
		{"port-forward rule 1 protocol", configtree.Single("icmp"), "not one of"},
		{"service nat rule 1 type", configtree.Single("hairpin"), "not one of"},
	}
	for _, tc := range cases {
		got := validateSet(mustPath(t, tc.path), tc.value)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
	}
}

func issuesFor(tree map[string]any) string {
	var msgs []string
	for _, i := range validateCommit(tree) {
		msgs = append(msgs, i.Path+": "+i.Msg)
	}
	return strings.Join(msgs, "\n")
}

func TestNATCommitChecks(t *testing.T) {
	t.Parallel()
	nat := func(rules map[string]any) map[string]any {
		return map[string]any{"service": map[string]any{"nat": map[string]any{"rule": rules}}}
	}
	cases := []struct {
		name string
		tree map[string]any
		want []string
	}{
		{"valid", nat(map[string]any{
			"10": map[string]any{"type": "destination", "inbound-interface": "eth0", "protocol": "tcp",
				"destination": map[string]any{"port": "443"}, "inside-address": map[string]any{"address": "192.168.1.2"}},
			"5000": map[string]any{"type": "masquerade", "outbound-interface": "eth0"},
			"5010": map[string]any{"type": "source", "outbound-interface": "eth0", "outside-address": map[string]any{"address": "203.0.113.1"}},
			"5020": map[string]any{"type": "source", "outbound-interface": "eth0", "exclude": nil},
		}), nil},
		{"destination", nat(map[string]any{
			"10": map[string]any{"type": "destination", "inbound-interface": "eth0", "outbound-interface": "eth1",
				"outside-address": map[string]any{"address": "203.0.113.1"}},
		}), []string{"outbound-interface not applicable", "outside-address not applicable", "inside-address not specified"}},
		{"source", nat(map[string]any{
			"5000": map[string]any{"type": "source", "outbound-interface": "eth0", "inbound-interface": "eth1",
				"inside-address": map[string]any{"address": "192.168.1.2"}},
		}), []string{"inbound-interface not applicable for source", "inside-address not applicable for source", "outside-address not specified"}},
		{"masquerade", nat(map[string]any{
			"5000": map[string]any{"type": "masquerade", "outbound-interface": "eth0",
				"outside-address": map[string]any{"address": "203.0.113.1"}, "source": map[string]any{"port": "53"}},
		}), []string{"should not be defined for masquerade", "ports can only be specified"}},
		{"port-forward", map[string]any{"port-forward": map[string]any{
			"wan-interface": "eth0", "lan-interface": []string{"eth1"},
			"rule": map[string]any{"1": map[string]any{"protocol": "tcp"}},
		}}, []string{"original-port must be set", "forward-to address must be set"}},
		{"static", map[string]any{"protocols": map[string]any{"static": map[string]any{
			"route":           map[string]any{"192.0.2.0/24": map[string]any{"next-hop": map[string]any{}}, "198.51.100.0/24": map[string]any{"blackhole": nil}},
			"interface-route": map[string]any{"10.0.0.0/8": map[string]any{}},
		}}}, []string{"route 192.0.2.0/24: Must define next-hop or blackhole", "interface-route 10.0.0.0/8: Must define next-hop-interface"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := issuesFor(tc.tree)
			if len(tc.want) == 0 && got != "" {
				t.Fatalf("unexpected issues:\n%s", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
		})
	}
}
