// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
)

func TestLoginUserMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		model  loginUserModel
		leaves map[string]configtree.Value
	}{
		{
			name: "new password",
			model: loginUserModel{
				Level: types.StringValue("operator"), FullName: types.StringValue("Alice Example"),
				PlaintextPassword: types.StringValue("s3cret!"), EncryptedPassword: types.StringUnknown(),
			},
			leaves: map[string]configtree.Value{
				"level":                             configtree.Single("operator"),
				"full-name":                         configtree.Single("Alice Example"),
				"authentication plaintext-password": configtree.Single("s3cret!"),
			},
		},
		{
			name: "unchanged password",
			model: loginUserModel{
				Level: types.StringValue("admin"), FullName: types.StringNull(),
				PlaintextPassword: types.StringValue("s3cret!"), EncryptedPassword: types.StringValue("$6$x$y"),
			},
			leaves: map[string]configtree.Value{"level": configtree.Single("admin")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			assertLeaves(t, loginUserToLeaves(ctx, &tc.model, &diags), tc.leaves)
		})
	}
}

func TestLoginUserFromTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tree := map[string]any{
		"level": "admin", "full-name": "Alice Example",
		"authentication": map[string]any{"encrypted-password": "$6$a$b", "plaintext-password": ""},
	}

	// After create: the hash is learned, the password kept.
	m := loginUserModel{PlaintextPassword: types.StringValue("s3cret!"), EncryptedPassword: types.StringUnknown(), AllowDestroyProviderUser: types.BoolNull()}
	loginUserFromTree(ctx, tree, &m, nil)
	if m.EncryptedPassword.ValueString() != "$6$a$b" || m.PlaintextPassword.ValueString() != "s3cret!" ||
		m.Level.ValueString() != "admin" || m.FullName.ValueString() != "Alice Example" || m.AllowDestroyProviderUser.ValueBool() {
		t.Fatalf("after create: %+v", m)
	}

	// Same hash on refresh: nothing changes.
	loginUserFromTree(ctx, tree, &m, nil)
	if m.PlaintextPassword.IsNull() {
		t.Fatal("password forgotten although the hash did not change")
	}

	// Hash changed outside Terraform: the password is forgotten.
	tree["authentication"] = map[string]any{"encrypted-password": "$6$c$d", "plaintext-password": ""}
	loginUserFromTree(ctx, tree, &m, nil)
	if !m.PlaintextPassword.IsNull() || m.EncryptedPassword.ValueString() != "$6$c$d" {
		t.Fatalf("after out of band change: %+v", m)
	}
}

func TestLoginUserImport(t *testing.T) {
	t.Parallel()
	for id, ok := range map[string]bool{"alice": true, "_svc": true, "ops-1": true, "Alice": false, "1abc": false, "": false, "a b": false} {
		if _, err := loginUserImport(id); (err == nil) != ok {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

// Acceptance tests against the fake router.

func TestAccLoginUser(t *testing.T) {
	e := newAccEnv(t)
	hash := func(path string, want *string) func() {
		return func() {
			v, _ := e.srv.Get(cliPath(path))
			s, _ := v.(string)
			*want = s
		}
	}
	var firstHash string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			e.destroyed("system login user alice", "system login user bob"),
			// The provider's own user and the rest of system are untouched.
			e.checkValue("system login user admin level", "admin"),
			e.checkValue("system host-name", "ubnt"),
		),
		Steps: []resource.TestStep{
			{
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  plaintext_password = "correct-horse"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_login_user.alice", "id", "alice"),
					resource.TestCheckResourceAttr("edgeos_system_login_user.alice", "level", "admin"),
					resource.TestCheckResourceAttrSet("edgeos_system_login_user.alice", "encrypted_password"),
					// EdgeOS hashes the password and keeps a blank plaintext.
					e.checkValue("system login user alice", map[string]any{
						"authentication": map[string]any{"encrypted-password": "$6$fake$" + fakeHash("correct-horse"), "plaintext-password": ""},
						"full-name":      "Alice Example",
						"level":          "admin",
					}),
				),
			},
			{
				// Changing the level does not send the password again.
				PreConfig: func() {
					hash("system login user alice authentication encrypted-password", &firstHash)()
					e.set("system login user alice authentication public-keys k1 type", configtree.Single("ssh-ed25519"))()
				},
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  level              = "operator"
  plaintext_password = "correct-horse"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.alice", plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue("edgeos_system_login_user.alice", tfjsonpath.New("encrypted_password"), knownvalue.StringExact("$6$fake$"+fakeHash("correct-horse"))),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system login user alice level", "operator"),
					e.checkValue("system login user alice authentication public-keys k1 type", "ssh-ed25519"),
					func(*terraform.State) error {
						if b := e.srv.LastBatch(); hasPath(b, "SET", "system", "login", "user", "alice", "authentication") {
							t.Errorf("the password was sent again: %v", b)
						}
						return nil
					},
				),
			},
			{
				// A new password gets a new hash in the same apply.
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  level              = "operator"
  plaintext_password = "a-much-longer-passphrase"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.alice", plancheck.ResourceActionUpdate),
					plancheck.ExpectUnknownValue("edgeos_system_login_user.alice", tfjsonpath.New("encrypted_password")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_login_user.alice", "encrypted_password", "$6$fake$"+fakeHash("a-much-longer-passphrase")),
					e.checkValue("system login user alice authentication plaintext-password", ""),
				),
			},
			{
				ResourceName:      "edgeos_system_login_user.alice",
				ImportState:       true,
				ImportStateVerify: true,
				// The password is write only and cannot be read back.
				ImportStateVerifyIgnore: []string{"plaintext_password"},
			},
			{
				// Drift: the password is changed by hand, the level too.
				PreConfig: func() {
					e.set("system login user alice authentication encrypted-password", configtree.Single("$6$other$hash"))()
					e.set("system login user alice level", configtree.Single("admin"))()
				},
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  level              = "operator"
  plaintext_password = "a-much-longer-passphrase"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.alice", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system login user alice level", "operator"),
					e.checkValue("system login user alice authentication encrypted-password", "$6$fake$"+fakeHash("a-much-longer-passphrase")),
				),
			},
			{
				// Drift: the user is deleted by hand and gets recreated.
				PreConfig: e.del("system login user alice"),
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  level              = "operator"
  plaintext_password = "a-much-longer-passphrase"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.alice", plancheck.ResourceActionCreate),
				}},
				Check: e.checkValue("system login user alice level", "operator"),
			},
			{
				// A new name replaces the user.
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "bob"
  plaintext_password = "a-much-longer-passphrase"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.alice", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkAbsent("system login user alice"),
					e.checkValue("system login user bob level", "admin"),
				),
			},
		},
	})
	if firstHash == "" {
		t.Fatal("the first hash was not captured")
	}
}

// TestAccLoginUserProviderUser imports the user the provider logs in as and
// checks that destroy keeps it unless allowed.
func TestAccLoginUserProviderUser(t *testing.T) {
	e := newAccEnv(t)
	cfg := func(e *accEnv, allow string) string {
		return e.config(`
import {
  to = edgeos_system_login_user.admin
  id = "` + fakeedgeos.DefaultUsername + `"
}
resource "edgeos_system_login_user" "admin" {
  name                        = "` + fakeedgeos.DefaultUsername + `"
  plaintext_password          = "` + fakeedgeos.DefaultPassword + `"
  allow_destroy_provider_user = ` + allow + `
}`)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.checkValue("system login user admin level", "admin"),
		Steps: []resource.TestStep{
			{
				Config: cfg(e, "false"),
				Check:  e.checkValue("system login user admin authentication plaintext-password", ""),
			},
		},
	})

	e2 := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e2.destroyed("system login user admin"),
		Steps: []resource.TestStep{
			{Config: cfg(e2, "true")},
		},
	})
}

func TestAccLoginUserErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig: func() { e.srv.FailNextCommit("useradd: failed") },
				Config: e.config(`
resource "edgeos_system_login_user" "x" {
  name               = "carol"
  plaintext_password = "pass-word"
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*useradd: failed`),
			},
			{
				// Existing users have to be imported.
				Config: e.config(`
resource "edgeos_system_login_user" "x" {
  name               = "admin"
  plaintext_password = "pass-word"
}`),
				ExpectError: regexp.MustCompile(`(?s)already exists.*Import it`),
			},
		},
	})
	if err := e.checkAbsent("system login user carol")(nil); err != nil {
		t.Fatal(err)
	}
}

func TestAccLoginUserValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		`name = "carol"
  plaintext_password = "onlyletters1"`: `not a letter or a digit`,
		`name = "carol"
  plaintext_password = "has\"quote"`: `double quote`,
		`name = "Carol"
  plaintext_password = "pass-word"`: `user name such as`,
		`name = "carol"
  level = "root"
  plaintext_password = "pass-word"`: `value must be one of`,
		`name = "carol"`: `plaintext_password.*is required`,
		`name = "carol"
  plaintext_password = "pass-word"
  encrypted_password = "$6$x$y"`: `Invalid Configuration for Read-Only Attribute`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_system_login_user\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)` + strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}

// fakeHash is the hash suffix the fake router computes for a password.
func fakeHash(pw string) string { return fmt.Sprintf("%x", len(pw)*7919) }

// hasPath reports whether the nested document doc has the node at names.
func hasPath(doc map[string]any, names ...string) bool {
	var cur any = doc
	for _, n := range names {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[n]; !ok {
			return false
		}
	}
	return true
}
