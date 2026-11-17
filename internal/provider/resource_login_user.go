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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	userNameRE    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	nonAlnumRE    = regexp.MustCompile(`[^A-Za-z0-9]`)
	attrPlaintext = path.Root("plaintext_password")
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

// hashFollowsPassword keeps the known hash in the plan unless the password
// changes, in which case the router computes a new one.
type hashFollowsPassword struct{}

func (hashFollowsPassword) Description(context.Context) string {
	return "keeps the hash unless plaintext_password changes"
}

func (m hashFollowsPassword) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (hashFollowsPassword) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.Plan.Raw.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	var planned, prior types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attrPlaintext, &planned)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, attrPlaintext, &prior)...)
	if planned.Equal(prior) {
		resp.PlanValue = req.StateValue
	}
}

func loginUserSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "A local login user (`system login user <name>`).\n\n" +
			"The password is write only: Terraform sends `plaintext_password` to the router, EdgeOS stores only a " +
			"hash (`encrypted_password`) and blanks the plaintext on commit. The configured password is kept in the " +
			"Terraform state, so protect the state. When the hash on the router changes outside Terraform, the next " +
			"plan sets the configured password again.\n\n" +
			"Options this resource does not model, such as SSH keys below `authentication public-keys`, are left " +
			"alone on update. Destroying the resource deletes the whole user, SSH keys included.\n\n" +
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
				MarkdownDescription: "Password of the user. EdgeOS 3.x requires at least one character that is not a letter or a digit. " +
					"It is never read back from the router.",
				Required:  true,
				Sensitive: true,
				Validators: []validator.String{
					edgeosValue(),
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(nonAlnumRE, "must contain at least one character that is not a letter or a digit"),
				},
			},
			"encrypted_password": schema.StringAttribute{
				MarkdownDescription: "Password hash the router stores, read from the router.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{hashFollowsPassword{}},
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
	// The hash is unknown exactly when the password is new or changed (see
	// hashFollowsPassword). Otherwise the password is left out, so an update
	// never compares it with the blank plaintext-password the router keeps.
	if m.EncryptedPassword.IsUnknown() {
		putString(l, m.PlaintextPassword, "authentication", "plaintext-password")
	}
	return l
}

func loginUserFromTree(_ context.Context, tree any, m *loginUserModel, _ *diag.Diagnostics) {
	m.Level = getString(tree, "level")
	m.FullName = getString(tree, "full-name")
	hash := getString(tree, "authentication", "encrypted-password")
	if known(m.EncryptedPassword) && !hash.Equal(m.EncryptedPassword) {
		// Changed outside Terraform: forget the password so the next plan
		// writes the configured one again.
		m.PlaintextPassword = types.StringNull()
	}
	m.EncryptedPassword = hash
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
