// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

const testSSHKey = "AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"

func TestValidateSetAccess(t *testing.T) {
	t.Parallel()
	pk := "system login user alice authentication public-keys alice@laptop "
	cases := []struct {
		path string
		val  configtree.Value
		want string // substring of the error, "" for accepted
	}{
		{"service ssh port", configtree.Single("2222"), ""},
		{"service ssh port", configtree.Single("0"), "range 1 to 65535"},
		{"service ssh port", configtree.Single("65536"), "range 1 to 65535"},
		{"service ssh protocol-version", configtree.Single("v2"), ""},
		{"service ssh protocol-version", configtree.Single("v3"), "not one of"},
		{"service ssh listen-address", configtree.Multi("192.168.1.1", "2001:db8::1"), ""},
		{"service ssh listen-address", configtree.Multi("192.168.1.0/24"), "not a valid IPv4 or IPv6 address"},
		{"service ssh disable-password-authentication", configtree.Flag(), ""},
		{"service gui https-port", configtree.Single("8443"), ""},
		{"service gui http-port", configtree.Single("http"), "range 1 to 65535"},
		{"service gui older-ciphers", configtree.Single("yes"), "not one of"},
		{"service gui listen-address", configtree.Multi("gw.example"), "not a valid IPv4 or IPv6 address"},
		{"service gui cert-file", configtree.Single("/config/auth/gui.pem"), ""},
		{"service gui ca-file", configtree.Single("ca.pem"), "absolute file name"},
		{pk + "type", configtree.Single("ssh-ed25519"), ""},
		{pk + "type", configtree.Single("ssh-ed448"), "not one of"},
		{pk + "key", configtree.Single(testSSHKey), ""},
		{pk + "key", configtree.Single("ssh-ed25519 " + testSSHKey), "only the base64 part"},
		{pk + "key", configtree.Single("not*base64"), "only the base64 part"},
	}
	for _, tc := range cases {
		got := validateSet(cliPath(t, tc.path), tc.val)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s = %v: got %q, want %q", tc.path, tc.val, got, tc.want)
		}
	}
	if !isMultiNode(cliPath(t, "service ssh listen-address")) || !isMultiNode(cliPath(t, "service gui listen-address")) {
		t.Error("listen-address is multi-valued")
	}
}

func TestValidateCommitAccess(t *testing.T) {
	t.Parallel()
	tree := configtree.Normalize(map[string]any{
		"service": map[string]any{
			"gui": map[string]any{"http-port": "8443", "https-port": "8443"},
			"ssh": map[string]any{"port": "8443"},
		},
		"system": map[string]any{"login": map[string]any{"user": map[string]any{"alice": map[string]any{
			"authentication": map[string]any{"public-keys": map[string]any{
				"ok":      map[string]any{"type": "ssh-ed25519", "key": testSSHKey},
				"no-type": map[string]any{"key": testSSHKey},
				"no-key":  map[string]any{"type": "ssh-rsa"},
			}},
		}}}},
	}).(map[string]any)
	var msgs []string
	for _, is := range checkAccessCommit(tree) {
		msgs = append(msgs, is.Path+": "+is.Msg)
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{
		"public-keys no-type: Must specify public key type",
		"public-keys no-key: Must specify public key value",
		"service gui: http-port and https-port must be different",
		"service ssh: Port 8443 is already in use by the web UI",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(msgs) != 4 {
		t.Errorf("got %d issues:\n%s", len(msgs), got)
	}
	if issues := checkAccessCommit(DefaultTree()); len(issues) != 0 {
		t.Errorf("default tree: %v", issues)
	}
}
