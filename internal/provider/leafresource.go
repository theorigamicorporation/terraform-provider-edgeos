// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// leafSpec describes a resource that owns a fixed set of leaves below a base
// path. Most EdgeOS resources fit this shape; the generic CRUD below handles
// existence checks, diffs, read-back and import for all of them.
//
// Ownership rules:
//   - Create, Update and Delete only touch leaves that ToLeaves produces for
//     the plan or the prior state. Anything else below the base path (rules
//     below a firewall name, unmodelled options) is left alone.
//   - By default the resource is a tag instance: Create fails if the node
//     exists and Delete removes the whole node.
//   - With Adopt, Create takes over an existing node and Delete only removes
//     the owned leaves (system settings, port-forward settings).
//   - With MustExist, the node has to exist already (physical interfaces);
//     it implies Adopt.
//   - Owned extends ownership to nodes ToLeaves does not produce (a flag
//     configured as false, every NTP server), and Reset makes Delete write a
//     default instead of deleting a leaf the node cannot be without.
type leafSpec[M any] struct {
	// Name is the type name without the provider prefix, e.g. "firewall_name".
	Name   string
	Schema schema.Schema
	// Path returns the base path for a model whose key attributes are set.
	Path func(m *M) configtree.Path
	// ID returns the resource ID for a model whose key attributes are set.
	ID func(m *M) string
	// SetID stores the ID in the model.
	SetID func(m *M, id string)
	// ToLeaves converts the model into leaves relative to Path. Null and
	// unknown attributes produce no leaves.
	ToLeaves func(ctx context.Context, m *M, diags *diag.Diagnostics) configtree.Leaves
	// FromTree updates the readable attributes of m from the subtree at Path.
	FromTree func(ctx context.Context, tree any, m *M, diags *diag.Diagnostics)
	// ImportAttrs parses an import ID into the key attributes.
	ImportAttrs func(id string) (map[string]attr.Value, error)
	// ImportHint is shown in the import error, e.g. "<ruleset>:<number>".
	ImportHint string
	// Adopt makes Create take over an existing node and Delete remove only
	// the owned leaves.
	Adopt bool
	// MustExist refuses to create the node; it implies Adopt.
	MustExist bool
	// Validate runs extra plan-time checks across attributes.
	Validate func(ctx context.Context, m *M, diags *diag.Diagnostics)
	// Owned lists nodes the resource owns although ToLeaves may have no leaf
	// for them, relative to Path. Create and Update delete them from the
	// device unless ToLeaves sets them, and Delete removes them. A path that
	// ends in "*" stands for every tag instance below its parent, so a
	// resource can own a whole list of tags (all NTP servers). Typical use:
	// a valueless node configured as false on an adopted node.
	Owned func(m *M) []configtree.Path
	// Reset returns values that Delete writes instead of deleting an owned
	// leaf, for leaves the node must always carry (duplex and speed of a
	// physical port). Only used with Adopt or MustExist.
	Reset func(m *M) configtree.Leaves
}

type resourceFactory = func() resource.Resource

type leafResource[M any] struct {
	spec   *leafSpec[M]
	client *client.Client
}

var (
	_ resource.ResourceWithConfigure      = (*leafResource[struct{}])(nil)
	_ resource.ResourceWithImportState    = (*leafResource[struct{}])(nil)
	_ resource.ResourceWithValidateConfig = (*leafResource[struct{}])(nil)
)

func newLeafResource[M any](spec *leafSpec[M]) func() resource.Resource {
	return func() resource.Resource { return &leafResource[M]{spec: spec} }
}

func (r *leafResource[M]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.spec.Name
}

func (r *leafResource[M]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.spec.Schema
}

func (r *leafResource[M]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *leafResource[M]) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if r.spec.Validate == nil {
		return
	}
	var m M
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.spec.Validate(ctx, &m, &resp.Diagnostics)
}

func clientFrom(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	pd, ok := data.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. This is a bug in the provider.", data))
		return nil
	}
	return pd.client
}

// ownedOnDevice returns the device leaves below base that are in keys. A key
// that is a flag (a valueless node or a tag instance such as an NTP server)
// also counts as present when the device has children below it, so removing
// it from the config deletes the whole node.
func ownedOnDevice(device configtree.Leaves, keys ...configtree.Leaves) configtree.Leaves {
	out := configtree.NewLeaves()
	for _, k := range keys {
		for _, p := range k.Paths() {
			if v, ok := device.Get(p); ok {
				out.Set(p, v)
				continue
			}
			if kv, _ := k.Get(p); kv.Kind == configtree.KindFlag && device.HasUnder(p) {
				out.Set(p, configtree.Flag())
			}
		}
	}
	return out
}

// ownedKeys expands the Owned paths of m against the device leaves.
func (r *leafResource[M]) ownedKeys(device configtree.Leaves, m *M) configtree.Leaves {
	out := configtree.NewLeaves()
	if r.spec.Owned == nil {
		return out
	}
	for _, p := range r.spec.Owned(m) {
		if p.Last() != "*" {
			out.SetFlag(p)
			continue
		}
		parent := p.Parent()
		for _, dp := range device.Paths() {
			if len(dp) > len(parent) && dp.HasPrefix(parent) {
				out.SetFlag(parent.Child(dp[len(parent)]))
			}
		}
	}
	return out
}

func (r *leafResource[M]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	base := r.spec.Path(&plan)
	device, exists, err := r.client.ReadLeaves(ctx, base)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", base, err)
		return
	}
	adopt := r.spec.Adopt || r.spec.MustExist
	if exists && !adopt {
		resp.Diagnostics.AddError("Already exists",
			fmt.Sprintf("%q already exists on the router. Import it instead of creating it, for example with an import block using id %q.",
				base.String(), r.spec.ID(&plan)))
		return
	}
	if !exists && r.spec.MustExist {
		resp.Diagnostics.AddError("Not found",
			fmt.Sprintf("%q does not exist on the router, and this resource can only manage existing nodes.", base.String()))
		return
	}
	desired := r.spec.ToLeaves(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	b := configtree.Diff(base, ownedOnDevice(device, desired, r.ownedKeys(device, &plan)), desired)
	if !exists && b.Empty() {
		// A tag instance without settings, such as a bare VLAN interface.
		b.Set(base, configtree.Flag())
	}
	if !r.apply(ctx, base, b, &resp.Diagnostics) {
		return
	}
	r.spec.SetID(&plan, r.spec.ID(&plan))
	if !r.readInto(ctx, base, &plan, &resp.Diagnostics) {
		resp.Diagnostics.AddError("Created node not found",
			fmt.Sprintf("%q was committed but cannot be read back.", base.String()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *leafResource[M]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	base := r.spec.Path(&state)
	if !r.readInto(ctx, base, &state, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			tflog.Info(ctx, "node is gone from the router, removing from state", map[string]any{"path": base.String()})
			resp.State.RemoveResource(ctx)
		}
		return
	}
	r.spec.SetID(&state, r.spec.ID(&state))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *leafResource[M]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	base := r.spec.Path(&plan)
	device, _, err := r.client.ReadLeaves(ctx, base)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", base, err)
		return
	}
	prior := r.spec.ToLeaves(ctx, &state, &resp.Diagnostics)
	desired := r.spec.ToLeaves(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	b := configtree.Diff(base, ownedOnDevice(device, prior, desired, r.ownedKeys(device, &state), r.ownedKeys(device, &plan)), desired)
	if !r.apply(ctx, base, b, &resp.Diagnostics) {
		return
	}
	r.spec.SetID(&plan, r.spec.ID(&plan))
	if !r.readInto(ctx, base, &plan, &resp.Diagnostics) {
		resp.Diagnostics.AddError("Updated node not found", fmt.Sprintf("%q disappeared during the update.", base.String()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *leafResource[M]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	base := r.spec.Path(&state)
	device, exists, err := r.client.ReadLeaves(ctx, base)
	if err != nil {
		addClientError(&resp.Diagnostics, "read", base, err)
		return
	}
	if !exists {
		return
	}
	b := configtree.NewBatch()
	if r.spec.Adopt || r.spec.MustExist {
		owned := ownedOnDevice(device, r.spec.ToLeaves(ctx, &state, &resp.Diagnostics), r.ownedKeys(device, &state))
		reset := configtree.NewLeaves()
		if r.spec.Reset != nil {
			reset = r.spec.Reset(&state)
		}
		for _, p := range owned.Paths() {
			if v, ok := reset.Get(p); ok {
				if cur, _ := owned.Get(p); !cur.Equal(v) {
					b.Set(base.Child(p...), v)
				}
				continue
			}
			b.Delete(base.Child(p...))
		}
	} else {
		b.Delete(base)
	}
	r.apply(ctx, base, b, &resp.Diagnostics)
}

func (r *leafResource[M]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	attrs, err := r.spec.ImportAttrs(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Cannot import %q: %s. Expected %s.", req.ID, err, r.spec.ImportHint))
		return
	}
	for name, v := range attrs {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(name), v)...)
	}
}

// readInto reads the base path and updates m. It returns false when the node
// does not exist or reading failed (diags tell which).
func (r *leafResource[M]) readInto(ctx context.Context, base configtree.Path, m *M, diags *diag.Diagnostics) bool {
	tree, ok, err := r.client.Read(ctx, base)
	if err != nil {
		addClientError(diags, "read", base, err)
		return false
	}
	if !ok {
		return false
	}
	r.spec.FromTree(ctx, tree, m, diags)
	return !diags.HasError()
}

func (r *leafResource[M]) apply(ctx context.Context, base configtree.Path, b *configtree.Batch, diags *diag.Diagnostics) bool {
	if b.Empty() {
		return true
	}
	tflog.Debug(ctx, "applying batch", map[string]any{"path": base.String(), "commands": b.String()})
	if err := r.client.Apply(ctx, b); err != nil {
		addClientError(diags, "commit", base, err)
		return false
	}
	return true
}

func addClientError(diags *diag.Diagnostics, op string, base configtree.Path, err error) {
	var ce *client.CommitError
	switch {
	case errors.As(err, &ce):
		diags.AddError("EdgeOS rejected the change",
			fmt.Sprintf("Changing %q failed and nothing was committed.\n\n%s", base.String(), err))
	case errors.Is(err, client.ErrAuth):
		diags.AddError("EdgeOS login failed",
			fmt.Sprintf("%s\n\nCheck username and password (or EDGEOS_USERNAME / EDGEOS_PASSWORD).", err))
	default:
		diags.AddError(fmt.Sprintf("Cannot %s %q", op, base.String()), err.Error())
	}
}
