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
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/shacrypt"
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
			leaves: map[string]configtree.Value{
				"level":                             configtree.Single("admin"),
				"authentication encrypted-password": configtree.Single("$6$x$y"),
			},
		},
		{
			name: "hash only",
			model: loginUserModel{
				Level: types.StringValue("admin"), PlaintextPassword: types.StringNull(), EncryptedPassword: types.StringValue("$6$x$y"),
			},
			leaves: map[string]configtree.Value{
				"level":                             configtree.Single("admin"),
				"authentication encrypted-password": configtree.Single("$6$x$y"),
			},
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
	// The hash is learned and the write-only password never kept.
	m := loginUserModel{PlaintextPassword: types.StringValue("s3cret!"), EncryptedPassword: types.StringUnknown(), AllowDestroyProviderUser: types.BoolNull()}
	loginUserFromTree(ctx, tree, &m, nil)
	if m.EncryptedPassword.ValueString() != "$6$a$b" || !m.PlaintextPassword.IsNull() ||
		m.Level.ValueString() != "admin" || m.FullName.ValueString() != "Alice Example" || m.AllowDestroyProviderUser.ValueBool() {
		t.Fatalf("after read: %+v", m)
	}
}

func TestPasswordMatches(t *testing.T) {
	t.Parallel()
	h := shacrypt.Crypt512("s3cret!", "Rt3xQ9", 0)
	for _, tc := range []struct {
		pw   string
		hash types.String
		want bool
	}{
		{"s3cret!", types.StringValue(h), true},
		{"s3cret?", types.StringValue(h), false},
		{"s3cret!", types.StringValue(shacrypt.Crypt256("s3cret!", "abc", 0)), true},
		{"s3cret!", types.StringValue("$1$abc$def"), false},
		{"s3cret!", types.StringNull(), false},
		{"s3cret!", types.StringUnknown(), false},
	} {
		if got := passwordMatches(tc.pw, tc.hash); got != tc.want {
			t.Errorf("passwordMatches(%q, %v) = %v", tc.pw, tc.hash, got)
		}
	}
}

// loginUserValue builds a value of the login user schema from the given
// attributes; the rest are null. A nil map gives a null object.
func loginUserValue(t *testing.T, s schema.Schema, attrs map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	typ := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if attrs == nil {
		return tftypes.NewValue(typ, nil)
	}
	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
		if v, ok := attrs[name]; ok {
			vals[name] = v
		}
	}
	return tftypes.NewValue(typ, vals)
}

func TestPasswordHashModifier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := loginUserSchema()
	h := shacrypt.Crypt512("s3cret!", "Rt3xQ9", 0)
	str := func(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	for _, tc := range []struct {
		name          string
		config, state map[string]tftypes.Value
		planned       types.String
		want          types.String
		wantErr       bool
	}{
		{name: "create with a password", config: map[string]tftypes.Value{"plaintext_password": str("s3cret!")},
			planned: types.StringUnknown(), want: types.StringUnknown()},
		{name: "create without a password", config: map[string]tftypes.Value{},
			planned: types.StringUnknown(), want: types.StringUnknown(), wantErr: true},
		{name: "imported, password matches", config: map[string]tftypes.Value{"plaintext_password": str("s3cret!")},
			state: map[string]tftypes.Value{"encrypted_password": str(h)}, planned: types.StringValue(h), want: types.StringValue(h)},
		{name: "imported, other changes, password matches", config: map[string]tftypes.Value{"plaintext_password": str("s3cret!")},
			state: map[string]tftypes.Value{"encrypted_password": str(h)}, planned: types.StringUnknown(), want: types.StringValue(h)},
		{name: "password differs", config: map[string]tftypes.Value{"plaintext_password": str("other-one!")},
			state: map[string]tftypes.Value{"encrypted_password": str(h)}, planned: types.StringValue(h), want: types.StringUnknown()},
		{name: "password unknown until apply", config: map[string]tftypes.Value{"plaintext_password": unknown},
			state: map[string]tftypes.Value{"encrypted_password": str(h)}, planned: types.StringValue(h), want: types.StringUnknown()},
		{name: "no password configured", config: map[string]tftypes.Value{},
			state: map[string]tftypes.Value{"encrypted_password": str(h)}, planned: types.StringUnknown(), want: types.StringValue(h)},
		{name: "hash configured", config: map[string]tftypes.Value{"encrypted_password": str("$6$new$hash")},
			state: map[string]tftypes.Value{"encrypted_password": str(h)}, planned: types.StringValue("$6$new$hash"), want: types.StringValue("$6$new$hash")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tfsdk.Config{Schema: s, Raw: loginUserValue(t, s, tc.config)}
			plan := tfsdk.Plan{Schema: s, Raw: loginUserValue(t, s, map[string]tftypes.Value{})}
			state := tfsdk.State{Schema: s, Raw: loginUserValue(t, s, tc.state)}
			var configValue, stateValue types.String
			cfg.GetAttribute(ctx, attrHash, &configValue)
			state.GetAttribute(ctx, attrHash, &stateValue)
			req := planmodifier.StringRequest{
				Path: attrHash, Config: cfg, Plan: plan, State: state,
				ConfigValue: configValue, PlanValue: tc.planned, StateValue: stateValue,
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			passwordHash{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Fatalf("planned %v, want %v", resp.PlanValue, tc.want)
			}
		})
	}
}

func TestLoginUserUpgradeState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newLoginUserResource().(*loginUserResource)
	up := r.UpgradeState(ctx)[0]
	str := func(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	prior := tfsdk.State{Schema: *up.PriorSchema, Raw: loginUserValue(t, *up.PriorSchema, map[string]tftypes.Value{
		"id": str("alice"), "name": str("alice"), "level": str("admin"),
		"plaintext_password": str("s3cret!"), "encrypted_password": str("$6$a$b"),
		"allow_destroy_provider_user": tftypes.NewValue(tftypes.Bool, false),
	})}
	s := loginUserSchema()
	resp := &fwresource.UpgradeStateResponse{State: tfsdk.State{Schema: s, Raw: loginUserValue(t, s, nil)}}
	up.StateUpgrader(ctx, fwresource.UpgradeStateRequest{State: &prior}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var m loginUserModel
	resp.State.Get(ctx, &m)
	if !m.PlaintextPassword.IsNull() || m.EncryptedPassword.ValueString() != "$6$a$b" || m.Name.ValueString() != "alice" {
		t.Fatalf("upgraded: %+v", m)
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
						"authentication": map[string]any{"encrypted-password": fakeHash("correct-horse"), "plaintext-password": ""},
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
					e.set("system login user alice authentication public-keys k1 key", configtree.Single(testEd25519Key))()
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
					plancheck.ExpectKnownValue("edgeos_system_login_user.alice", tfjsonpath.New("encrypted_password"), knownvalue.StringExact(fakeHash("correct-horse"))),
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
					resource.TestCheckResourceAttr("edgeos_system_login_user.alice", "encrypted_password", fakeHash("a-much-longer-passphrase")),
					e.checkValue("system login user alice authentication plaintext-password", ""),
				),
			},
			{
				ResourceName:      "edgeos_system_login_user.alice",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// An import block with the same config plans nothing: the
				// password matches the hash, so it is not sent again.
				ResourceName:     "edgeos_system_login_user.alice",
				ImportState:      true,
				ImportStateKind:  resource.ImportBlockWithID,
				ImportPlanChecks: resource.ImportPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// So does the config Terraform generates for it, which has
				// no password at all.
				ResourceName:     "edgeos_system_login_user.alice",
				ImportState:      true,
				ImportStateKind:  resource.ImportBlockWithID,
				GenerateConfig:   true,
				ImportPlanChecks: resource.ImportPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// The same password set by hand gets a new salt, which is
				// not a change.
				PreConfig: e.set("system login user alice authentication encrypted-password",
					configtree.Single(shacrypt.Crypt512("a-much-longer-passphrase", "byhand", 0))),
				Config: e.config(`
resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  level              = "operator"
  plaintext_password = "a-much-longer-passphrase"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
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
					e.checkValue("system login user alice authentication encrypted-password", fakeHash("a-much-longer-passphrase")),
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

// TestAccLoginUserImportExisting imports a user created outside Terraform,
// the way a real router's users are, and checks that the plan after the
// import is empty whether the config has the right password, the hash or
// neither, and that only a wrong password is sent.
func TestAccLoginUserImportExisting(t *testing.T) {
	e := newAccEnv(t)
	// A real router picks a random salt.
	routerHash := shacrypt.Crypt512("pass-word!", "Rt3xQ9kL", 0)
	e.set("system login user carol level", configtree.Single("admin"))()
	e.set("system login user carol authentication encrypted-password", configtree.Single(routerHash))()
	e.set("system login user carol authentication plaintext-password", configtree.Single(""))()
	newHash := shacrypt.Crypt512("third-pass!", "x7Yt", 0)
	cfg := func(attrs string) string {
		return e.config(`
import {
  to = edgeos_system_login_user.carol
  id = "carol"
}
resource "edgeos_system_login_user" "carol" {
  name = "carol"
  ` + attrs + `
}`)
	}
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	noBatch := func(*terraform.State) error {
		if n := e.srv.Stats().Batches; n != 0 {
			return fmt.Errorf("%d batches sent to the router", n)
		}
		return nil
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             e.destroyed("system login user carol"),
		Steps: []resource.TestStep{
			{
				Config:           cfg(`plaintext_password = "pass-word!"`),
				ConfigPlanChecks: empty,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_system_login_user.carol", "encrypted_password", routerHash),
					resource.TestCheckNoResourceAttr("edgeos_system_login_user.carol", "plaintext_password"),
					noBatch,
				),
			},
			{Config: cfg(`encrypted_password = "` + routerHash + `"`), ConfigPlanChecks: empty, Check: noBatch},
			{Config: cfg(``), ConfigPlanChecks: empty, Check: noBatch},
			{
				// A wrong password is a real change.
				Config: cfg(`plaintext_password = "other-pass!"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.carol", plancheck.ResourceActionUpdate),
					plancheck.ExpectUnknownValue("edgeos_system_login_user.carol", tfjsonpath.New("encrypted_password")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("system login user carol authentication encrypted-password", fakeHash("other-pass!")),
					e.checkValue("system login user carol authentication plaintext-password", ""),
				),
			},
			{
				// A configured hash is written as is.
				Config: cfg(`encrypted_password = "` + newHash + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_system_login_user.carol", plancheck.ResourceActionUpdate),
				}},
				Check: e.checkValue("system login user carol authentication", map[string]any{
					"encrypted-password": newHash, "plaintext-password": "",
				}),
			},
			{
				// Switching to the password behind that hash changes nothing.
				Config:           cfg(`plaintext_password = "third-pass!"`),
				ConfigPlanChecks: empty,
			},
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
		`name = "carol"`: `Missing password`,
		`name = "carol"
  plaintext_password = "pass-word"
  encrypted_password = "$6$x$y"`: `Invalid Attribute Combination`,
		`name = "carol"
  encrypted_password = "not-a-hash"`: `must be a crypt.3. hash`,
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

// fakeHash is the hash the fake router stores for a password.
func fakeHash(pw string) string { return fakeedgeos.HashPassword(pw) }

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
