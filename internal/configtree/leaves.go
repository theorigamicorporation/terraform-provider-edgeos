// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package configtree

import (
	"sort"
)

// Leaves is a flat set of leaf values keyed by path. Resources describe the
// part of the config they own as Leaves relative to a base path.
type Leaves struct {
	m map[string]Value
}

// NewLeaves returns an empty set.
func NewLeaves() Leaves {
	return Leaves{m: map[string]Value{}}
}

// Set stores v at path, replacing any previous value.
func (l Leaves) Set(path Path, v Value) {
	l.m[path.key()] = v
}

// SetSingle stores a single value.
func (l Leaves) SetSingle(path Path, s string) { l.Set(path, Single(s)) }

// SetMulti stores a multi value.
func (l Leaves) SetMulti(path Path, values ...string) { l.Set(path, Multi(values...)) }

// SetFlag stores a valueless node.
func (l Leaves) SetFlag(path Path) { l.Set(path, Flag()) }

// Get returns the value at path.
func (l Leaves) Get(path Path) (Value, bool) {
	v, ok := l.m[path.key()]
	return v, ok
}

// Has reports whether path is set.
func (l Leaves) Has(path Path) bool {
	_, ok := l.m[path.key()]
	return ok
}

// Delete removes path from the set.
func (l Leaves) Delete(path Path) {
	delete(l.m, path.key())
}

// Len returns the number of leaves.
func (l Leaves) Len() int { return len(l.m) }

// Paths returns all paths in a stable order.
func (l Leaves) Paths() []Path {
	keys := make([]string, 0, len(l.m))
	for k := range l.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Path, 0, len(keys))
	for _, k := range keys {
		out = append(out, pathFromKey(k))
	}
	return out
}

// Under returns the leaves below prefix, with paths relative to it.
func (l Leaves) Under(prefix Path) Leaves {
	out := NewLeaves()
	for _, p := range l.Paths() {
		if p.HasPrefix(prefix) && len(p) > len(prefix) {
			v, _ := l.Get(p)
			out.Set(NewPath(p[len(prefix):]...), v)
		}
	}
	return out
}

// HasUnder reports whether any leaf lives below prefix.
func (l Leaves) HasUnder(prefix Path) bool {
	for k := range l.m {
		if pathFromKey(k).HasPrefix(prefix) && len(pathFromKey(k)) > len(prefix) {
			return true
		}
	}
	return false
}

// Equal reports whether both sets hold the same paths and values.
func (l Leaves) Equal(o Leaves) bool {
	if l.Len() != o.Len() {
		return false
	}
	for k, v := range l.m {
		ov, ok := o.m[k]
		if !ok || !v.Equal(ov) {
			return false
		}
	}
	return true
}

// Diff computes the batch that turns old into new below base. Leaves that are
// gone or changed are deleted; leaves that are new or changed are set. A
// changed multi-valued leaf is deleted and set again so the whole list is
// replaced, which works because EdgeOS applies DELETE before SET.
//
// A flag that turns into a value (or the other way round) is handled the same
// way. Leaves that are equal are left alone, so an update never touches
// config the resource does not own.
func Diff(base Path, old, updated Leaves) *Batch {
	b := NewBatch()
	for _, p := range old.Paths() {
		ov, _ := old.Get(p)
		nv, ok := updated.Get(p)
		if !ok {
			b.Delete(base.Child(p...))
			continue
		}
		if ov.Equal(nv) {
			continue
		}
		if ov.Kind == KindMulti || ov.Kind != nv.Kind {
			b.Delete(base.Child(p...))
		}
	}
	for _, p := range updated.Paths() {
		nv, _ := updated.Get(p)
		ov, ok := old.Get(p)
		if ok && ov.Equal(nv) {
			continue
		}
		b.Set(base.Child(p...), nv)
	}
	return b
}
