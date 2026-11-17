// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package configtree

import (
	"sort"
)

// A config tree is the decoded JSON used by the EdgeOS API: map[string]any
// for interior nodes, string for single values, []any or []string for multi
// values and nil for valueless nodes.

// Normalize returns a deep copy of a decoded tree with multi values as
// []string and empty strings at interior positions turned into nil. EdgeOS
// returns "" for empty top level nodes.
func Normalize(x any) any {
	switch t := x.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = Normalize(v)
		}
		return out
	case []any:
		vals := make([]string, 0, len(t))
		for _, e := range t {
			vals = append(vals, scalarString(e))
		}
		return vals
	case []string:
		return append([]string{}, t...)
	}
	return x
}

// Lookup walks the tree along path. It returns false when a node on the way
// does not exist or is not an interior node.
func Lookup(tree any, path Path) (any, bool) {
	cur := tree
	for _, name := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[name]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// Children returns the sorted names of the children of an interior node, or
// nil if x is not one.
func Children(x any) []string {
	m, ok := x.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Flatten turns a subtree into a set of leaves with paths relative to the
// subtree root. An empty interior node is reported as a flag so its presence
// is not lost. A bare leaf at the root is reported with an empty path.
func Flatten(x any) Leaves {
	l := NewLeaves()
	flatten(x, Path{}, l)
	return l
}

func flatten(x any, at Path, l Leaves) {
	m, ok := x.(map[string]any)
	if !ok {
		v, _ := ValueFromJSON(x)
		l.Set(at, v)
		return
	}
	if len(m) == 0 {
		if len(at) > 0 {
			l.Set(at, Flag())
		}
		return
	}
	for k, v := range m {
		flatten(v, at.Child(k), l)
	}
}

// Build turns leaves back into a nested tree. It is the inverse of Flatten.
func Build(l Leaves) any {
	if v, ok := l.Get(Path{}); ok && l.Len() == 1 {
		return v.JSON()
	}
	root := map[string]any{}
	for _, p := range l.Paths() {
		if len(p) == 0 {
			continue
		}
		v, _ := l.Get(p)
		insertSet(root, p, v.JSON())
	}
	return root
}

// insertSet places val at path inside root, creating interior nodes on the
// way. A nil value never replaces an existing subtree, because the presence
// of a node is implied by its children.
func insertSet(root map[string]any, path Path, val any) {
	cur := root
	for i, name := range path {
		if i == len(path)-1 {
			if _, isMap := cur[name].(map[string]any); isMap && val == nil {
				return
			}
			cur[name] = val
			return
		}
		next, ok := cur[name].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[name] = next
		}
		cur = next
	}
}

// insertDelete marks path for deletion. Marking a node removes any marks
// below it, and nothing is added below a node that is already marked.
func insertDelete(root map[string]any, path Path) {
	cur := root
	for i, name := range path {
		if i == len(path)-1 {
			cur[name] = nil
			return
		}
		existing, present := cur[name]
		if present && existing == nil {
			return
		}
		next, ok := existing.(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[name] = next
		}
		cur = next
	}
}

// DeepCopy returns a deep copy of a normalized tree.
func DeepCopy(x any) any {
	return Normalize(x)
}

// SetIn sets val at path inside a tree and returns the new root, creating
// interior nodes as needed. It is meant for tests and the fake server.
func SetIn(tree map[string]any, path Path, v Value) {
	cur := tree
	for i, name := range path {
		if i == len(path)-1 {
			switch v.Kind {
			case KindFlag:
				if _, isMap := cur[name].(map[string]any); isMap {
					return
				}
				cur[name] = nil
			case KindSingle:
				cur[name] = v.String()
			case KindMulti:
				existing, _ := cur[name].([]string)
				for _, nv := range v.Values {
					found := false
					for _, e := range existing {
						if e == nv {
							found = true
							break
						}
					}
					if !found {
						existing = append(existing, nv)
					}
				}
				cur[name] = existing
			}
			return
		}
		next, ok := cur[name].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[name] = next
		}
		cur = next
	}
}

// DeleteIn removes the node at path and prunes interior nodes that become
// empty, the way EdgeOS does. It reports whether anything was removed.
func DeleteIn(tree map[string]any, path Path) bool {
	if len(path) == 0 {
		return false
	}
	parents := make([]map[string]any, 0, len(path))
	cur := tree
	for _, name := range path[:len(path)-1] {
		parents = append(parents, cur)
		next, ok := cur[name].(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	if _, ok := cur[path.Last()]; !ok {
		return false
	}
	delete(cur, path.Last())
	// Prune empty parents bottom up, but never the top level nodes.
	for i := len(path) - 2; i >= 1; i-- {
		node := parents[i]
		child, _ := node[path[i]].(map[string]any)
		if len(child) > 0 {
			break
		}
		delete(node, path[i])
	}
	return true
}
