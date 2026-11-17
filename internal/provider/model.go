// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Helpers that move values between framework types and config leaves. The
// put* helpers skip null and unknown values; the get* helpers return null
// when the node is absent, so attributes that are not on the device read as
// null and Terraform notices drift.

func known(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

func putString(l configtree.Leaves, v types.String, names ...string) {
	if known(v) {
		l.SetSingle(configtree.NewPath(names...), v.ValueString())
	}
}

func putInt(l configtree.Leaves, v types.Int64, names ...string) {
	if known(v) {
		l.SetSingle(configtree.NewPath(names...), strconv.FormatInt(v.ValueInt64(), 10))
	}
}

// putFlag sets a valueless node when v is true.
func putFlag(l configtree.Leaves, v types.Bool, names ...string) {
	if known(v) && v.ValueBool() {
		l.SetFlag(configtree.NewPath(names...))
	}
}

// putEnable writes "enable" or "disable".
func putEnable(l configtree.Leaves, v types.Bool, names ...string) {
	if known(v) {
		s := "disable"
		if v.ValueBool() {
			s = "enable"
		}
		l.SetSingle(configtree.NewPath(names...), s)
	}
}

// putBoolString writes "true" or "false".
func putBoolString(l configtree.Leaves, v types.Bool, names ...string) {
	if known(v) {
		l.SetSingle(configtree.NewPath(names...), strconv.FormatBool(v.ValueBool()))
	}
}

func putStrings(ctx context.Context, l configtree.Leaves, v interface {
	attr.Value
	ElementsAs(context.Context, any, bool) diag.Diagnostics
}, diags *diag.Diagnostics, names ...string) {
	if !known(v) {
		return
	}
	var vals []string
	diags.Append(v.ElementsAs(ctx, &vals, false)...)
	if len(vals) == 0 {
		return
	}
	if _, isSet := v.(types.Set); isSet {
		sort.Strings(vals)
		l.Set(configtree.NewPath(names...), configtree.UnorderedMulti(vals...))
		return
	}
	l.SetMulti(configtree.NewPath(names...), vals...)
}

// putTag creates a tag instance without children, such as an NTP server.
func putTags(ctx context.Context, l configtree.Leaves, v types.Set, diags *diag.Diagnostics, names ...string) {
	if !known(v) {
		return
	}
	var vals []string
	diags.Append(v.ElementsAs(ctx, &vals, false)...)
	for _, s := range vals {
		l.SetFlag(configtree.NewPath(names...).Child(s))
	}
}

func lookup(tree any, names ...string) (any, bool) {
	return configtree.Lookup(tree, configtree.NewPath(names...))
}

func getString(tree any, names ...string) types.String {
	v, ok := lookup(tree, names...)
	if !ok {
		return types.StringNull()
	}
	val, isLeaf := configtree.ValueFromJSON(v)
	if !isLeaf {
		return types.StringNull()
	}
	switch val.Kind {
	case configtree.KindSingle:
		return types.StringValue(val.String())
	case configtree.KindMulti:
		if len(val.Values) > 0 {
			return types.StringValue(val.Values[0])
		}
	}
	return types.StringNull()
}

func getInt(tree any, diags *diag.Diagnostics, names ...string) types.Int64 {
	s := getString(tree, names...)
	if s.IsNull() {
		return types.Int64Null()
	}
	n, err := strconv.ParseInt(s.ValueString(), 10, 64)
	if err != nil {
		diags.AddError("Unexpected value on the router",
			fmt.Sprintf("%q is %q, expected a number.", configtree.NewPath(names...).String(), s.ValueString()))
		return types.Int64Null()
	}
	return types.Int64Value(n)
}

// getFlag is true when the valueless node exists.
func getFlag(tree any, names ...string) types.Bool {
	_, ok := lookup(tree, names...)
	return types.BoolValue(ok)
}

// getEnable maps "enable"/"disable" to a bool, null when absent.
func getEnable(tree any, names ...string) types.Bool {
	s := getString(tree, names...)
	if s.IsNull() {
		return types.BoolNull()
	}
	return types.BoolValue(s.ValueString() == "enable")
}

// getBoolString maps "true"/"false" to a bool, null when absent.
func getBoolString(tree any, names ...string) types.Bool {
	s := getString(tree, names...)
	if s.IsNull() {
		return types.BoolNull()
	}
	b, err := strconv.ParseBool(s.ValueString())
	if err != nil {
		return types.BoolNull()
	}
	return types.BoolValue(b)
}

func multiValues(tree any, names ...string) ([]string, bool) {
	v, ok := lookup(tree, names...)
	if !ok {
		return nil, false
	}
	val, isLeaf := configtree.ValueFromJSON(v)
	if !isLeaf || val.Kind == configtree.KindFlag {
		return nil, false
	}
	return val.Values, true
}

func getSet(tree any, names ...string) types.Set {
	vals, ok := multiValues(tree, names...)
	if !ok {
		return types.SetNull(types.StringType)
	}
	return stringSet(vals)
}

func getList(tree any, names ...string) types.List {
	vals, ok := multiValues(tree, names...)
	if !ok {
		return types.ListNull(types.StringType)
	}
	elems := make([]attr.Value, 0, len(vals))
	for _, s := range vals {
		elems = append(elems, types.StringValue(s))
	}
	return types.ListValueMust(types.StringType, elems)
}

// getTags returns the names of the tag instances below names.
func getTags(tree any, names ...string) types.Set {
	v, ok := lookup(tree, names...)
	if !ok {
		return types.SetNull(types.StringType)
	}
	return stringSet(configtree.Children(v))
}

func stringSet(vals []string) types.Set {
	elems := make([]attr.Value, 0, len(vals))
	for _, s := range vals {
		elems = append(elems, types.StringValue(s))
	}
	return types.SetValueMust(types.StringType, elems)
}

// hasAny reports whether any of the named children exist below tree.
func hasAny(tree any, names ...string) bool {
	for _, n := range names {
		if _, ok := lookup(tree, n); ok {
			return true
		}
	}
	return false
}
