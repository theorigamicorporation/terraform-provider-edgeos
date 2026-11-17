// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package configtree

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"unicode/utf8"
)

// Kind says what sort of leaf a Value is.
type Kind int

const (
	// KindFlag is a valueless node such as "disable", or a tag instance
	// without children. The EdgeOS JSON API encodes it as null.
	KindFlag Kind = iota
	// KindSingle is a leaf with exactly one value, encoded as a string.
	KindSingle
	// KindMulti is a multi-valued leaf such as "address", encoded as an array.
	KindMulti
)

func (k Kind) String() string {
	switch k {
	case KindFlag:
		return "flag"
	case KindSingle:
		return "single"
	case KindMulti:
		return "multi"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Value is the content of a leaf node.
type Value struct {
	Kind   Kind
	Values []string
	// Unordered marks a multi value whose order does not matter (it comes
	// from a Terraform set). Comparisons then ignore order.
	Unordered bool
}

// Flag returns a valueless leaf.
func Flag() Value { return Value{Kind: KindFlag} }

// Single returns a single-valued leaf.
func Single(s string) Value { return Value{Kind: KindSingle, Values: []string{s}} }

// Multi returns a multi-valued leaf. The order of values is kept.
func Multi(values ...string) Value {
	return Value{Kind: KindMulti, Values: append([]string{}, values...)}
}

// String returns the value of a single-valued leaf, or "" otherwise.
func (v Value) String() string {
	if v.Kind == KindSingle && len(v.Values) == 1 {
		return v.Values[0]
	}
	return ""
}

// Equal reports whether both values have the same kind and values in the same
// order. If either side is Unordered the order is ignored.
func (v Value) Equal(o Value) bool {
	if v.Unordered || o.Unordered {
		return v.EqualUnordered(o)
	}
	return v.Kind == o.Kind && slices.Equal(v.Values, o.Values)
}

// UnorderedMulti returns a multi value whose order does not matter.
func UnorderedMulti(values ...string) Value {
	v := Multi(values...)
	v.Unordered = true
	return v
}

// EqualUnordered is like Equal but ignores the order of multi values.
func (v Value) EqualUnordered(o Value) bool {
	if v.Kind != o.Kind || len(v.Values) != len(o.Values) {
		return false
	}
	a, b := slices.Clone(v.Values), slices.Clone(o.Values)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}

// JSON returns the representation used by the EdgeOS JSON API.
func (v Value) JSON() any {
	switch v.Kind {
	case KindSingle:
		return v.String()
	case KindMulti:
		return append([]string{}, v.Values...)
	}
	return nil
}

// ValueFromJSON converts a decoded JSON leaf into a Value. It returns false
// for objects, which are interior nodes rather than leaves.
func ValueFromJSON(x any) (Value, bool) {
	switch t := x.(type) {
	case nil:
		return Flag(), true
	case string:
		return Single(t), true
	case []string:
		return Multi(t...), true
	case []any:
		vals := make([]string, 0, len(t))
		for _, e := range t {
			vals = append(vals, scalarString(e))
		}
		return Multi(vals...), true
	case map[string]any:
		return Value{}, false
	}
	return Single(scalarString(x)), true
}

func scalarString(x any) string {
	switch t := x.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
	}
	return fmt.Sprint(x)
}

// ValidateValue checks a leaf value against the rules EdgeOS applies to every
// value string. EdgeOS rejects double quotes ("Cannot use the double quote
// character in a value string") and the CLI cannot carry control characters
// other than tab.
func ValidateValue(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("value is not valid UTF-8")
	}
	for _, r := range s {
		switch {
		case r == '"':
			return errors.New(`EdgeOS does not allow the double quote character (") in a value`)
		case r == '\t':
		case r < 0x20 || r == 0x7f:
			return fmt.Errorf("value contains the control character %U", r)
		}
	}
	return nil
}
