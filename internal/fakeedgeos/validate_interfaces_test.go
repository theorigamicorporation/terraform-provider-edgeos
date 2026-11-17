// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestInterfaceSetChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		value configtree.Value
		want  string
	}{
		{"interfaces switch switch0 address", configtree.Multi("192.168.1.1/24", "dhcp"), ""},
		{"interfaces switch switch0 address", configtree.Multi("192.168.1.1"), "prefix length"},
		{"interfaces switch br0 description", configtree.Single("x"), "not a valid switch interface name"},
		{"interfaces switch switch0 switch-port interface eth2", configtree.Flag(), ""},
		{"interfaces switch switch0 switch-port interface wg0", configtree.Flag(), "not a valid ethernet interface name"},
		{"interfaces switch switch0 switch-port vlan-aware", configtree.Single("enable"), ""},
		{"interfaces switch switch0 switch-port vlan-aware", configtree.Single("yes"), "not one of"},
		{"interfaces switch switch0 mtu", configtree.Single("9001"), "MTU must be between"},
		{"interfaces switch switch0 mac", configtree.Single("02:00:5e"), "not a valid MAC"},
		{"interfaces switch switch0 dhcp-options default-route", configtree.Single("always"), "not one of"},
		{"interfaces switch switch0 dhcp-options default-route-distance", configtree.Single("0"), "Must be between 1-255"},
		{"interfaces ethernet eth0 dhcp-options client-option", configtree.Multi("send host-name router;"), ""},
		{"interfaces ethernet eth0 dhcp-options client-option", configtree.Multi(`send host-name "router";`), "double quote"},
		{"interfaces switch switch0 dhcp-options global-option", configtree.Multi(`option x code 77 = "y";`), "double quote"},
	}
	for _, tc := range cases {
		got := validateSet(mustPath(t, tc.path), tc.value)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
	}
	for _, p := range []string{
		"interfaces switch switch0 address",
		"interfaces switch switch0 dhcp-options client-option",
		"interfaces switch switch0 dhcp-options global-option",
		"interfaces ethernet eth0 vif 10 dhcp-options client-option",
	} {
		if !isMultiNode(mustPath(t, p)) {
			t.Errorf("%s should be multi-valued", p)
		}
	}
}

func TestSwitchPortCommitChecks(t *testing.T) {
	t.Parallel()
	ports := func(names ...string) map[string]any {
		m := map[string]any{}
		for _, n := range names {
			m[n] = nil
		}
		return map[string]any{"interface": m}
	}
	tree := DefaultTree()
	ifaces := tree["interfaces"].(map[string]any)
	ifaces["switch"] = map[string]any{"switch0": map[string]any{"switch-port": ports("eth2", "eth3")}}
	if got := issuesFor(tree); got != "" {
		t.Fatalf("valid switch: %s", got)
	}
	ifaces["switch"] = map[string]any{
		"switch0": map[string]any{"switch-port": ports("eth2", "eth9")},
		"switch1": map[string]any{"switch-port": ports("eth2")},
	}
	got := issuesFor(tree)
	for _, want := range []string{
		"interfaces switch switch0 switch-port interface eth9: Interface eth9 does not exist",
		"interfaces switch switch1 switch-port interface eth2: Interface eth2 is already a port of switch0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
