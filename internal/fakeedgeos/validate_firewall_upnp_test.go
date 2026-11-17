// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestFirewallUPnPSetChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		value configtree.Value
		want  string
	}{
		{"firewall all-ping", configtree.Single("disable"), ""},
		{"firewall ipv6-receive-redirects", configtree.Single("on"), "not one of"},
		{"firewall source-validation", configtree.Single("loose"), ""},
		{"firewall source-validation", configtree.Single("enable"), "not one of"},
		{"service dhcp-server static-arp", configtree.Single("true"), "not one of"},
		{"service upnp2 listen-on", configtree.Multi("switch0", "eth1.10"), ""},
		{"service upnp2 listen-on", configtree.Multi("LAN"), "not a valid interface name"},
		{"service upnp2 wan", configtree.Single("pppoe0"), ""},
		{"service upnp2 nat-pmp", configtree.Single("yes"), "not one of"},
		{"service upnp2 secure-mode", configtree.Single("enable"), ""},
		{"service upnp2 acl rule 0 action", configtree.Single("allow"), "between 1 and 9999"},
		{"service upnp2 acl rule 010 action", configtree.Single("allow"), "between 1 and 9999"},
		{"service upnp2 acl rule 10 action", configtree.Single("permit"), "not one of"},
		{"service upnp2 acl rule 10 subnet", configtree.Single("192.168.1.0/24"), ""},
		{"service upnp2 acl rule 10 subnet", configtree.Single("192.168.1.1/24"), "Invalid network"},
		{"service upnp2 acl rule 10 external-port", configtree.Single("1024-65535"), ""},
		{"service upnp2 acl rule 10 local-port", configtree.Single("80"), ""},
		{"service upnp2 acl rule 10 local-port", configtree.Single("90-80"), "not a valid port"},
		{"service upnp2 acl rule 10 local-port", configtree.Single("70000"), "not a valid port"},
	}
	for _, tc := range cases {
		got := validateSet(mustPath(t, tc.path), tc.value)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
	}
	if !isMultiNode(mustPath(t, "service upnp2 listen-on")) {
		t.Error("listen-on is a multi node")
	}
}

func TestUPnPCommitChecks(t *testing.T) {
	t.Parallel()
	got := issuesFor(map[string]any{"service": map[string]any{"upnp2": map[string]any{
		"nat-pmp": "enable",
		"acl":     map[string]any{"rule": map[string]any{"10": map[string]any{"subnet": "192.168.1.0/24"}}},
	}}})
	for _, want := range []string{
		"service upnp2: listen-on must be set",
		"service upnp2: wan must be set",
		"service upnp2 acl rule 10: action must be set",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	ok := issuesFor(map[string]any{"service": map[string]any{"upnp2": map[string]any{
		"listen-on": []any{"switch0"}, "wan": "eth0",
	}}})
	if ok != "" {
		t.Errorf("valid upnp2 refused: %s", ok)
	}
}
