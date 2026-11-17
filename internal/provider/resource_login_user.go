// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/shacrypt"
)

var (
	userNameRE    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	nonAlnumRE    = regexp.MustCompile(`[^A-Za-z0-9]`)
	cryptHashRE   = regexp.MustCompile(`^\$[0-9a-z]+\$\S+$`)
	attrPlaintext = path.Root("plaintext_password")
	attrHash      = path.Root("encrypted_password")
)

type loginUserModel struct {
	ID                       types.String `tfsdk:"id"`
	Name                     types.String `tfsdk:"name"`
	Level                    types.String `tfsdk:"level"`
	FullName                 types.String `tfsdk:"full_name"`
	PlaintextPassword        types.String `tfsdk:"plaintext_password"`
	EncryptedPassword        types.String `tfsdk:"encrypted_password"`
	AllowDestroyProviderUser types.Bool   `tfsdk:"allow_destroy_provider_user"`
}

// passwordHash plans encrypted_password. A configured hash is used as is.
// Otherwise the hash the router has is kept while the configured
// plaintext_password still matches it, so neither an import nor a refresh
// rewrites a password that is already right. When it does not match, the
// hash becomes unknown and the update sends the password.
type passwordHash struct{}

func (passwordHash) Description(context.Context) string {
	return "keeps the router's hash while plaintext_password matches it"
}

func (m passwordHash) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (passwordHash) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.Plan.Raw.IsNull() || !req.ConfigValue.IsNull() {
		return
	}
	var pw types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, attrPlaintext, &pw)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		if pw.IsNull() {
			resp.Diagnostics.AddAttributeError(attrPlaintext, "Missing password",
				"A new login user needs plaintext_password or encrypted_password.")
		}
		return
	}
	switch {
	case pw.IsNull():
		// The password is not managed: keep whatever the router has.
		resp.PlanValue = req.StateValue
	case !pw.IsUnknown() && passwordMatches(pw.ValueString(), req.StateValue):
		resp.PlanValue = req.StateValue
	default:
		resp.PlanValue = types.StringUnknown()
	}
}

// passwordMatches reports whether pw hashes to the crypt(3) hash h. Hashes
// it cannot check (not $5$ or $6$) never match, so the password is set again
// and the router stores a hash that can be checked next time.
func passwordMatches(pw string, h types.String) bool {
	if !known(h) {
		return false
	}
	ok, err := shacrypt.Verify(pw, h.ValueString())
	return err == nil && ok
}

func loginUserSchema() schema.Schema {
	return schema.Schema{
		Version: 1,
		MarkdownDescription: "A local login user (`system login user <name>`).\n\n" +
			"`plaintext_password` is a write-only attribute: Terraform sends it to the router and never stores it in " +
			"the state. EdgeOS stores only a hash (`encrypted_password`) and blanks the plaintext on commit. Every plan " +
			"checks the configured password against that hash and plans an update only when it no longer matches, " +
			"so a password changed outside Terraform is set again, and an imported user whose password is already " +
			"right is left alone. Instead of a password you can configure `encrypted_password` directly, or leave " +
			"both out on an imported user to keep the password it has.\n\n" +
			"Options this resource does not model, such as SSH keys below `authentication public-keys`, are left " +
			"alone on update; manage keys with `edgeos_system_login_user_public_key`. Destroying the resource " +
			"deletes the whole user, SSH keys included.\n\n" +
			"The user the provider logs in as is protected: destroying it only removes it from the Terraform state " +
			"and leaves it on the router, unless `allow_destroy_provider_user = true`. Changing its `level` to " +
			"`operator` takes away the configuration rights the provider needs.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("The user name."),
			"name": schema.StringAttribute{
				MarkdownDescription: "User name: lower case letters, digits, `_` and `-`, starting with a letter or `_`, up to 32 characters. Changing it creates a new user.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(userNameRE, "must be a user name such as alice")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"level": schema.StringAttribute{
				MarkdownDescription: "`admin` (full access) or `operator` (read only). Defaults to `admin`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("admin"),
				Validators:          []validator.String{stringvalidator.OneOf("admin", "operator")},
			},
			"full_name": schema.StringAttribute{
				MarkdownDescription: "Full name of the user.",
				Optional:            true,
				Validators:          []validator.String{edgeosValue(), stringvalidator.LengthBetween(1, 255)},
			},
			"plaintext_password": schema.StringAttribute{
				MarkdownDescription: "Password of the user. Write only: it is sent to the router but never stored in the Terraform " +
					"state, and EdgeOS keeps only its hash. EdgeOS 3.x requires at least one character that is not a letter " +
					"or a digit. Each plan checks it against `encrypted_password` and only sends it when it does not match. " +
					"Conflicts with `encrypted_password`; a new user needs one of the two.",
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
				Validators: []validator.String{
					edgeosValue(),
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(nonAlnumRE, "must contain at least one character that is not a letter or a digit"),
					stringvalidator.ConflictsWith(path.MatchRoot("encrypted_password")),
				},
			},
			"encrypted_password": schema.StringAttribute{
				MarkdownDescription: "Password hash (crypt(3), `$6$...` on EdgeOS). Read from the router; set it to give the " +
					"user a hash instead of `plaintext_password`. When neither is configured, an existing user keeps the " +
					"password it has.",
				Optional:      true,
				Computed:      true,
				Sensitive:     true,
				Validators:    []validator.String{edgeosValue(), stringvalidator.RegexMatches(cryptHashRE, "must be a crypt(3) hash such as $6$salt$hash")},
				PlanModifiers: []planmodifier.String{passwordHash{}},
			},
			"allow_destroy_provider_user": schema.BoolAttribute{
				MarkdownDescription: "Let destroy delete this user even when the provider logs in as it. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
		},
	}
}

func loginUserToLeaves(_ context.Context, m *loginUserModel, _ *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putString(l, m.Level, "level")
	putString(l, m.FullName, "full-name")
	// The hash is unknown exactly when the password has to be sent (see
	// passwordHash); Create and Update copy it from the config. Otherwise the
	// hash is owned instead, and the blank plaintext-password the router
	// keeps is never compared with anything.
	if m.EncryptedPassword.IsUnknown() {
		putString(l, m.PlaintextPassword, "authentication", "plaintext-password")
	} else {
		putString(l, m.EncryptedPassword, "authentication", "encrypted-password")
	}
	return l
}

func loginUserFromTree(_ context.Context, tree any, m *loginUserModel, _ *diag.Diagnostics) {
	m.Level = getString(tree, "level")
	m.FullName = getString(tree, "full-name")
	m.EncryptedPassword = getString(tree, "authentication", "encrypted-password")
	// Write only: never kept in the state.
	m.PlaintextPassword = types.StringNull()
	if m.AllowDestroyProviderUser.IsNull() {
		m.AllowDestroyProviderUser = types.BoolValue(false)
	}
}

func loginUserImport(id string) (map[string]attr.Value, error) {
	if !userNameRE.MatchString(id) {
		return nil, fmt.Errorf("%q is not a valid user name", id)
	}
	return map[string]attr.Value{"name": types.StringValue(id)}, nil
}

var loginUserSpec = &leafSpec[loginUserModel]{
	Name:   "system_login_user",
	Schema: loginUserSchema(),
	Path: func(m *loginUserModel) configtree.Path {
		return configtree.NewPath("system", "login", "user", m.Name.ValueString())
	},
	ID:          func(m *loginUserModel) string { return m.Name.ValueString() },
	SetID:       func(m *loginUserModel, id string) { m.ID = types.StringValue(id) },
	ToLeaves:    loginUserToLeaves,
	FromTree:    loginUserFromTree,
	ImportAttrs: loginUserImport,
	ImportHint:  "the user name, for example alice",
}

// loginUserResource is the generic resource plus a guard that keeps destroy
// from deleting the user the provider logs in as.
type loginUserResource struct {
	*leafResource[loginUserModel]
	username string
}

func newLoginUserResource() resource.Resource {
	return &loginUserResource{leafResource: &leafResource[loginUserModel]{spec: loginUserSpec}}
}

func (r *loginUserResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.leafResource.Configure(ctx, req, resp)
	if pd, ok := req.ProviderData.(*providerData); ok {
		r.username = pd.username
	}
}

var _ resource.ResourceWithUpgradeState = (*loginUserResource)(nil)

// Create and Update take the write-only password from the config, as the
// plan never carries it.
func (r *loginUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	copyPassword(ctx, req.Config, &req.Plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.leafResource.Create(ctx, req, resp)
}

func (r *loginUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	copyPassword(ctx, req.Config, &req.Plan, &resp.Diagnostics)
	var planned types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attrHash, &planned)...)
	if planned.IsUnknown() {
		// The password is sent, so the old hash is no longer owned: the
		// commit replaces it, and deleting it in the same batch is not needed.
		resp.Diagnostics.Append(req.State.SetAttribute(ctx, attrHash, types.StringNull())...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	r.leafResource.Update(ctx, req, resp)
}

func copyPassword(ctx context.Context, config tfsdk.Config, plan *tfsdk.Plan, diags *diag.Diagnostics) {
	var pw types.String
	diags.Append(config.GetAttribute(ctx, attrPlaintext, &pw)...)
	diags.Append(plan.SetAttribute(ctx, attrPlaintext, pw)...)
}

// UpgradeState drops the password that version 0 kept in the state.
func (r *loginUserResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	prior := loginUserSchemaV0()
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &prior,
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var m loginUserModel
				resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
				if resp.Diagnostics.HasError() {
					return
				}
				m.PlaintextPassword = types.StringNull()
				resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			},
		},
	}
}

// loginUserSchemaV0 is the shape of the version 0 state, which stored
// plaintext_password.
func loginUserSchemaV0() schema.Schema {
	str := func(computed bool) schema.StringAttribute {
		return schema.StringAttribute{Optional: !computed, Computed: computed}
	}
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id":                          str(true),
		"name":                        schema.StringAttribute{Required: true},
		"level":                       schema.StringAttribute{Optional: true, Computed: true},
		"full_name":                   str(false),
		"plaintext_password":          schema.StringAttribute{Required: true, Sensitive: true},
		"encrypted_password":          schema.StringAttribute{Computed: true, Sensitive: true},
		"allow_destroy_provider_user": schema.BoolAttribute{Optional: true, Computed: true},
	}}
}

func (r *loginUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state loginUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Name.ValueString() == r.username && !state.AllowDestroyProviderUser.ValueBool() {
		resp.Diagnostics.AddWarning("Login user kept on the router",
			fmt.Sprintf("The provider logs in as %q, so destroy only removed it from the Terraform state. "+
				"Set allow_destroy_provider_user = true to delete it.", r.username))
		return
	}
	r.leafResource.Delete(ctx, req, resp)
}
