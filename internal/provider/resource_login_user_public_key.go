// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	publicKeyNameRE = regexp.MustCompile(`^[A-Za-z0-9@._+-]{1,255}$`)
	publicKeyRE     = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
)

// publicKeyTypes are the key types EdgeOS accepts for login users.
var publicKeyTypes = []string{
	"ssh-rsa", "ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-dss",
}

type loginUserPublicKeyModel struct {
	ID      types.String `tfsdk:"id"`
	User    types.String `tfsdk:"user"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	Key     types.String `tfsdk:"key"`
	Options types.String `tfsdk:"options"`
}

func loginUserPublicKeySchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "An SSH public key of a login user (`system login user <user> authentication public-keys <name>`).\n\n" +
			"Keys are separate from `edgeos_system_login_user` so they can be added and rotated on their own; the user " +
			"resource leaves `public-keys` alone. The user has to exist already: reference `edgeos_system_login_user` " +
			"in `user` so Terraform creates it first. Destroying the key deletes only this key.\n\n" +
			"`key` is the base64 part of the key only, without the type in front or the comment at the end. For a line " +
			"`ssh-ed25519 AAAAC3Nza... alice@laptop` set `type = \"ssh-ed25519\"`, `key = \"AAAAC3Nza...\"` and use the " +
			"comment as `name`.\n\n" +
			"Before you set `disable_password_authentication` on `edgeos_service_ssh`, make sure the key works, or " +
			"you lose SSH access to the router.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("`<user>:<name>`, for example `alice:alice@laptop`."),
			"user": schema.StringAttribute{
				MarkdownDescription: "Login user the key belongs to. Changing it moves the key to another user.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(userNameRE, "must be a user name such as alice")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the key, usually the comment of the key such as `alice@laptop`: letters, digits " +
					"and `@`, `.`, `_`, `+`, `-`. Changing it creates a new key.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.RegexMatches(publicKeyNameRE, "must be a key name such as alice@laptop, without spaces or ':'")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Key type: `ssh-ed25519`, `ssh-rsa`, `ecdsa-sha2-nistp256`, `ecdsa-sha2-nistp384`, " +
					"`ecdsa-sha2-nistp521` or `ssh-dss`. It has to match the type encoded in `key`.",
				Required:   true,
				Validators: []validator.String{stringvalidator.OneOf(publicKeyTypes...)},
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "The base64 encoded key, without type and comment.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(publicKeyRE, "must be the base64 part of the key, without spaces, type or comment")},
			},
			"options": schema.StringAttribute{
				MarkdownDescription: "Options in front of the key in `authorized_keys`, such as `no-agent-forwarding,no-port-forwarding`. " +
					"EdgeOS cannot store double quotes, so options like `from=\"...\"` are not possible.",
				Optional:   true,
				Validators: []validator.String{edgeosValue(), stringvalidator.LengthBetween(1, 1024)},
			},
		},
	}
}

// publicKeyBlobType returns the key type encoded at the start of an SSH
// public key blob.
func publicKeyBlobType(key string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", fmt.Errorf("not valid base64")
	}
	if len(blob) < 4 {
		return "", fmt.Errorf("too short for an SSH key")
	}
	n := binary.BigEndian.Uint32(blob)
	if n == 0 || n > 64 || int(n) > len(blob)-4 {
		return "", fmt.Errorf("does not start with a key type")
	}
	return string(blob[4 : 4+n]), nil
}

func validateLoginUserPublicKey(_ context.Context, m *loginUserPublicKeyModel, diags *diag.Diagnostics) {
	if !known(m.Key) || !publicKeyRE.MatchString(m.Key.ValueString()) {
		return
	}
	got, err := publicKeyBlobType(m.Key.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("key"), "Invalid SSH public key",
			fmt.Sprintf("The key is %s. Use the second field of the public key line.", err))
		return
	}
	if known(m.Type) && got != m.Type.ValueString() {
		diags.AddAttributeError(path.Root("type"), "Key type mismatch",
			fmt.Sprintf("type is %q but the key is a %q key.", m.Type.ValueString(), got))
	}
}

func loginUserPublicKeyImport(id string) (map[string]attr.Value, error) {
	user, name, ok := strings.Cut(id, ":")
	if !ok {
		return nil, fmt.Errorf("missing ':'")
	}
	if !userNameRE.MatchString(user) {
		return nil, fmt.Errorf("%q is not a valid user name", user)
	}
	if !publicKeyNameRE.MatchString(name) {
		return nil, fmt.Errorf("%q is not a valid key name", name)
	}
	return map[string]attr.Value{"user": types.StringValue(user), "name": types.StringValue(name)}, nil
}

var loginUserPublicKeySpec = &leafSpec[loginUserPublicKeyModel]{
	Name:   "system_login_user_public_key",
	Schema: loginUserPublicKeySchema(),
	Path: func(m *loginUserPublicKeyModel) configtree.Path {
		return configtree.NewPath("system", "login", "user", m.User.ValueString(), "authentication", "public-keys", m.Name.ValueString())
	},
	ID:    func(m *loginUserPublicKeyModel) string { return m.User.ValueString() + ":" + m.Name.ValueString() },
	SetID: func(m *loginUserPublicKeyModel, id string) { m.ID = types.StringValue(id) },
	ToLeaves: func(_ context.Context, m *loginUserPublicKeyModel, _ *diag.Diagnostics) configtree.Leaves {
		l := configtree.NewLeaves()
		putString(l, m.Type, "type")
		putString(l, m.Key, "key")
		putString(l, m.Options, "options")
		return l
	},
	FromTree: func(_ context.Context, tree any, m *loginUserPublicKeyModel, _ *diag.Diagnostics) {
		m.Type = getString(tree, "type")
		m.Key = getString(tree, "key")
		m.Options = getString(tree, "options")
	},
	ImportAttrs: loginUserPublicKeyImport,
	ImportHint:  "<user>:<name>, for example alice:alice@laptop",
	Validate:    validateLoginUserPublicKey,
}

// loginUserPublicKeyResource is the generic resource plus a check that the
// user exists, so a key never creates a user without a password.
type loginUserPublicKeyResource struct {
	*leafResource[loginUserPublicKeyModel]
}

func newLoginUserPublicKeyResource() resource.Resource {
	return &loginUserPublicKeyResource{leafResource: &leafResource[loginUserPublicKeyModel]{spec: loginUserPublicKeySpec}}
}

func (r *loginUserPublicKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan loginUserPublicKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user := configtree.NewPath("system", "login", "user", plan.User.ValueString())
	_, ok, err := r.client.Read(ctx, user)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", user, err)
		return
	}
	if !ok {
		resp.Diagnostics.AddAttributeError(path.Root("user"), "Login user not found",
			fmt.Sprintf("%q does not exist on the router. Create it with edgeos_system_login_user and reference that resource in user.",
				plan.User.ValueString()))
		return
	}
	r.leafResource.Create(ctx, req, resp)
}
