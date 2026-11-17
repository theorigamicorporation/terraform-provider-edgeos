// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestOwnedOnDevice(t *testing.T) {
	t.Parallel()
	device := configtree.Flatten(map[string]any{
		"host-name": "r1",
		"login":     map[string]any{"user": map[string]any{"admin": map[string]any{"level": "admin"}}},
		"ntp": map[string]any{"server": map[string]any{
			"a.example": nil,
			"b.example": map[string]any{"prefer": nil},
		}},
	})
	prior := configtree.NewLeaves()
	prior.SetSingle(cliPath("host-name"), "r0")
	prior.SetFlag(cliPath("ntp server a.example"))
	prior.SetFlag(cliPath("ntp server b.example"))
	desired := configtree.NewLeaves()
	desired.SetSingle(cliPath("host-name"), "r2")
	desired.SetSingle(cliPath("domain-name"), "example.net")

	owned := ownedOnDevice(device, prior, desired)
	want := map[string]configtree.Value{
		"host-name":            configtree.Single("r1"),
		"ntp server a.example": configtree.Flag(),
		"ntp server b.example": configtree.Flag(),
	}
	assertLeaves(t, owned, want)

	// Removing both servers deletes the nodes, children included, and
	// never touches the login users.
	b := configtree.Diff(cliPath("system"), owned, desired)
	got := b.String()
	wantCLI := "delete system ntp server a.example\ndelete system ntp server b.example\nset system domain-name example.net\nset system host-name r2"
	if got != wantCLI {
		t.Fatalf("batch:\n%s\nwant:\n%s", got, wantCLI)
	}
}

func TestKeepCovered(t *testing.T) {
	t.Parallel()
	// Owned facilities on the device: all and protocols stay configured, mail
	// goes away.
	owned := configtree.NewLeaves()
	for _, f := range []string{"all", "protocols", "mail"} {
		owned.SetFlag(cliPath("facility " + f))
	}
	owned.SetSingle(cliPath("facility all level"), "notice")
	owned.SetSingle(cliPath("facility protocols level"), "debug")
	desired := configtree.NewLeaves()
	desired.SetSingle(cliPath("facility all level"), "info")
	desired.SetSingle(cliPath("facility protocols level"), "debug")

	b := configtree.Diff(cliPath("system syslog global"), keepCovered(owned, desired), desired)
	want := "delete system syslog global facility mail\nset system syslog global facility all level info"
	if got := b.String(); got != want {
		t.Fatalf("batch:\n%s\nwant:\n%s", got, want)
	}
}
