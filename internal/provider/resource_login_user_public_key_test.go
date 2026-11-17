// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Made up keys with a valid structure: the type, then 32 or 64 counting bytes.
const (
	testEd25519Key = "AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
	testRSAKey     = "AAAAB3NzaC1yc2EAAAADAQABAAAAQQAAAQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyAhIiMkJSYnKCkqKywtLi8wMTIzNDU2Nzg5Ojs8PT4/"
)

func TestLoginUserPublicKeyMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := loginUserPublicKeySpec
	m := loginUserPublicKeyModel{
		User: types.StringValue("alice"), Name: types.StringValue("alice@laptop"),
		Type: types.StringValue("ssh-ed25519"), Key: types.StringValue(testEd25519Key),
		Options: types.StringValue("no-agent-forwarding"),
	}
	want := map[string]configtree.Value{
		"type":    configtree.Single("ssh-ed25519"),
		"key":     configtree.Single(testEd25519Key),
		"options": configtree.Single("no-agent-forwarding"),
	}
	var diags diag.Diagnostics
	l := spec.ToLeaves(ctx, &m, &diags)
	assertLeaves(t, l, want)
	if got := spec.Path(&m).String(); got != "system login user alice authentication public-keys alice@laptop" {
		t.Errorf("path = %s", got)
	}
	if spec.ID(&m) != "alice:alice@laptop" {
		t.Errorf("id = %s", spec.ID(&m))
	}
	back := loginUserPublicKeyModel{User: m.User, Name: m.Name}
	spec.FromTree(ctx, configtree.Build(l), &back, &diags)
	assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), want)

	m.Options = types.StringNull()
	delete(want, "options")
	assertLeaves(t, spec.ToLeaves(ctx, &m, &diags), want)
}

func TestLoginUserPublicKeyValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		typ, key string
		want     string
	}{
		{"ssh-ed25519", testEd25519Key, ""},
		{"ssh-rsa", testRSAKey, ""},
		{"ssh-rsa", testEd25519Key, `type is "ssh-rsa" but the key is a "ssh-ed25519" key`},
		{"ssh-ed25519", "AAAA", "too short"},
		{"ssh-ed25519", "AAAAAA==", "does not start with a key type"},
		{"ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5A", "not valid base64"},
		{"ssh-ed25519", "has spaces", ""}, // left to the attribute validator
	}
	for _, tc := range cases {
		var diags diag.Diagnostics
		m := loginUserPublicKeyModel{Type: types.StringValue(tc.typ), Key: types.StringValue(tc.key)}
		validateLoginUserPublicKey(context.Background(), &m, &diags)
		got := ""
		for _, d := range diags {
			got += d.Detail()
		}
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s %s: got %q, want %q", tc.typ, tc.key, got, tc.want)
		}
	}
}

func TestLoginUserPublicKeyImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{
		"alice:alice@laptop": true, "homaster:zone10_master": true, "alice:k1": true,
		"alice": false, "Alice:k1": false, "alice:": false, "alice:a b": false, ":k1": false,
	} {
		if _, err := loginUserPublicKeyImport(id); (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
	}
	attrs, err := loginUserPublicKeyImport("alice:a:b")
	if err == nil {
		t.Errorf("':' in the key name must fail: %v", attrs)
	}
}

// Acceptance tests against the fake router.

func publicKeyConfig(name, typ, key, extra string) string {
	return `
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  plaintext_password = "correct-horse"
}

resource "edgeos_system_login_user_public_key" "alice" {
  user = edgeos_system_login_user.alice.name
  name = "` + name + `"
  type = "` + typ + `"
  key  = "` + key + `"
  ` + extra + `
}`
}

func TestAccLoginUserPublicKey(t *testing.T) {
	e := newAccEnv(t)
	const keys = "system login user alice authentication public-keys"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system login user alice"),
			e.checkValue("system login user admin level", "admin"),
		),
		Steps: []resource.TestStep{
			{
				Config: e.config(publicKeyConfig("alice@laptop", "ssh-ed25519", testEd25519Key, "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_login_user_public_key.alice", "id", "alice:alice@laptop"),
					resource.TestCheckNoResourceAttr("edgeos_system_login_user_public_key.alice", "options"),
					e.checkValue(keys+" alice@laptop", map[string]any{"type": "ssh-ed25519", "key": testEd25519Key}),
				),
			},
			{
				// Rotating the key and adding options is an update; the
				// password is not sent again.
				Config: e.config(publicKeyConfig("alice@laptop", "ssh-rsa", testRSAKey, `options = "no-agent-forwarding,no-port-forwarding"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user_public_key.alice", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("edgeos_system_login_user.alice", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue(keys+" alice@laptop", map[string]any{
						"type": "ssh-rsa", "key": testRSAKey, "options": "no-agent-forwarding,no-port-forwarding",
					}),
					e.checkValue("system login user alice authentication encrypted-password", fakeHash("correct-horse")),
				),
			},
			{
				ResourceName:      "edgeos_system_login_user_public_key.alice",
				ImportState:       true,
				ImportStateId:     "alice:alice@laptop",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_system_login_user_public_key.alice",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
			},
			{
				// Drift: options removed and a second key added by hand. The
				// options come back, the other key stays.
				PreConfig: func() {
					e.del(keys + " alice@laptop options")()
					e.set(keys+" other type", configtree.Single("ssh-ed25519"))()
					e.set(keys+" other key", configtree.Single(testEd25519Key))()
				},
				Config: e.config(publicKeyConfig("alice@laptop", "ssh-rsa", testRSAKey, `options = "no-agent-forwarding,no-port-forwarding"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user_public_key.alice", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue(keys+" alice@laptop options", "no-agent-forwarding,no-port-forwarding"),
					e.checkValue(keys+" other type", "ssh-ed25519"),
				),
			},
			{
				// Drift: the key is deleted by hand and gets recreated.
				PreConfig: e.del(keys + " alice@laptop"),
				Config:    e.config(publicKeyConfig("alice@laptop", "ssh-rsa", testRSAKey, "")),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user_public_key.alice", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue(keys+" alice@laptop", map[string]any{"type": "ssh-rsa", "key": testRSAKey}),
			},
			{
				// A new name replaces the key and leaves the other one.
				Config: e.config(publicKeyConfig("alice@desktop", "ssh-ed25519", testEd25519Key, "")),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user_public_key.alice", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(keys+" alice@laptop"),
					e.checkValue(keys+" alice@desktop type", "ssh-ed25519"),
					e.checkValue(keys+" other type", "ssh-ed25519"),
				),
			},
			{
				// Destroying the key alone keeps the user.
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  plaintext_password = "correct-horse"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent(keys+" alice@desktop"),
					e.checkValue("system login user alice level", "admin"),
				),
			},
		},
	})
}

func TestAccLoginUserPublicKeyErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The user has to exist; a key never creates one.
				Config: e.config(`
resource "edgeos_system_login_user_public_key" "x" {
  user = "nobody"
  name = "k1"
  type = "ssh-ed25519"
  key  = "` + testEd25519Key + `"
}`),
				ExpectError: regexp.MustCompile(`(?s)Login user not found.*"nobody" does not exist`),
			},
			{
				PreConfig: func() {
					e.set("system login user admin authentication public-keys k1 type", configtree.Single("ssh-ed25519"))()
					e.set("system login user admin authentication public-keys k1 key", configtree.Single(testEd25519Key))()
				},
				Config: e.config(`
resource "edgeos_system_login_user_public_key" "x" {
  user = "admin"
  name = "k1"
  type = "ssh-ed25519"
  key  = "` + testEd25519Key + `"
}`),
				ExpectError: regexp.MustCompile(`(?s)already exists.*Import it.*admin:k1`),
			},
			{
				PreConfig: func() { e.srv.FailNextCommit("loadkey: failed") },
				Config: e.config(`
resource "edgeos_system_login_user_public_key" "x" {
  user = "admin"
  name = "k2"
  type = "ssh-ed25519"
  key  = "` + testEd25519Key + `"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*loadkey: failed`),
			},
		},
	})
	if err := e.checkAbsent("system login user nobody")(nil); err != nil {
		t.Fatal(err)
	}
	if err := e.checkAbsent("system login user admin authentication public-keys k2")(nil); err != nil {
		t.Fatal(err)
	}
}

func TestAccLoginUserPublicKeyValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		`user = "alice"
  name = "k1"
  type = "ssh-ed448"
  key = "` + testEd25519Key + `"`: `value must be one of`,
		`user = "alice"
  name = "k1"
  type = "ssh-ed25519"
  key = "ssh-ed25519 ` + testEd25519Key + `"`: `base64 part of the key`,
		`user = "alice"
  name = "k1"
  type = "ssh-rsa"
  key = "` + testEd25519Key + `"`: `Key type mismatch`,
		`user = "alice"
  name = "alice laptop"
  type = "ssh-ed25519"
  key = "` + testEd25519Key + `"`: `key name such as`,
		`user = "Alice"
  name = "k1"
  type = "ssh-ed25519"
  key = "` + testEd25519Key + `"`: `user name such as`,
		`user = "alice"
  name = "k1"
  type = "ssh-ed25519"
  key = "` + testEd25519Key + `"
  options = "from=\"192.0.2.0/24\""`: `double quote`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_system_login_user_public_key\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)` + strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
