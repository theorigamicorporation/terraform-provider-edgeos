// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package configtree

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPathString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path Path
		want string
	}{
		{"simple", NewPath("firewall", "name", "WAN_IN", "rule", "10"), "firewall name WAN_IN rule 10"},
		{"cidr", NewPath("protocols", "static", "route", "192.0.2.0/24"), "protocols static route 192.0.2.0/24"},
		{"base64", NewPath("peer", "aGVsbG8+/w=="), "peer aGVsbG8+/w=="},
		{"space", NewPath("description", "two words"), `description "two words"`},
		{"quote", NewPath(`say "hi"`), `"say \"hi\""`},
		{"backslash", NewPath(`a\b`), `"a\\b"`},
		{"newline", NewPath("a\nb"), `"a\nb"`},
		{"single quote", NewPath("it's"), `"it's"`},
		{"empty element", NewPath("a", ""), `a ""`},
		{"unicode", NewPath("café"), `"café"`},
		{"root", Path{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.path.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
			if len(tc.path) == 0 {
				return
			}
			back, err := ParsePath(tc.want)
			if err != nil {
				t.Fatalf("ParsePath(%q): %v", tc.want, err)
			}
			if !back.Equal(tc.path) {
				t.Fatalf("ParsePath(%q) = %#v, want %#v", tc.want, back, tc.path)
			}
		})
	}
}

func TestParsePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    Path
		wantErr string
	}{
		{in: "system host-name", want: NewPath("system", "host-name")},
		{in: "  system \t  host-name  ", want: NewPath("system", "host-name")},
		{in: `service 'dhcp server' x`, want: NewPath("service", "dhcp server", "x")},
		{in: `a"b c"d`, want: NewPath("ab cd")},
		{in: `""`, want: NewPath("")},
		{in: `"tab\there"`, want: NewPath("tab\there")},
		{in: "", wantErr: "must not be empty"},
		{in: "   ", wantErr: "must not be empty"},
		{in: `"open`, wantErr: "missing closing double quote"},
		{in: `'open`, wantErr: "missing closing single quote"},
		{in: `"bad \x"`, wantErr: "unknown escape"},
		{in: `"end\`, wantErr: "unfinished escape"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePath(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParsePath(%q) error = %v, want %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePath(%q): %v", tc.in, err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("ParsePath(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPathValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path    Path
		wantErr string
	}{
		{NewPath("system", "host-name"), ""},
		{Path{}, "must not be empty"},
		{NewPath("a", ""), "element 1 is empty"},
		{NewPath("a\x01"), "control character"},
		{NewPath(string([]byte{0xff})), "UTF-8"},
	}
	for _, tc := range cases {
		err := tc.path.Validate()
		if tc.wantErr == "" && err != nil {
			t.Errorf("Validate(%q) = %v", tc.path, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("Validate(%q) = %v, want %q", tc.path, err, tc.wantErr)
		}
	}
}

func TestPathHelpers(t *testing.T) {
	t.Parallel()
	p := NewPath("a", "b", "c")
	c := p.Child("d")
	if !c.Equal(NewPath("a", "b", "c", "d")) || len(p) != 3 {
		t.Fatalf("Child changed receiver or returned %v", c)
	}
	if !p.Parent().Equal(NewPath("a", "b")) || !(Path{}).Parent().Equal(Path{}) {
		t.Fatal("Parent")
	}
	if p.Last() != "c" || (Path{}).Last() != "" {
		t.Fatal("Last")
	}
	if !p.HasPrefix(NewPath("a", "b")) || p.HasPrefix(NewPath("b")) || p.HasPrefix(NewPath("a", "b", "c", "d")) {
		t.Fatal("HasPrefix")
	}
	if p.Equal(NewPath("a", "b", "x")) {
		t.Fatal("Equal")
	}
}

func TestValueFromJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     any
		want   Value
		isLeaf bool
	}{
		{"null", nil, Flag(), true},
		{"string", "accept", Single("accept"), true},
		{"array", []any{"192.0.2.1/24", "dhcp"}, Multi("192.0.2.1/24", "dhcp"), true},
		{"string slice", []string{"a"}, Multi("a"), true},
		{"number", float64(22), Single("22"), true},
		{"float", 1.5, Single("1.5"), true},
		{"bool", true, Single("true"), true},
		{"object", map[string]any{}, Value{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ValueFromJSON(tc.in)
			if ok != tc.isLeaf {
				t.Fatalf("leaf = %v, want %v", ok, tc.isLeaf)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestValueHelpers(t *testing.T) {
	t.Parallel()
	if Single("x").String() != "x" || Multi("x").String() != "" || Flag().String() != "" {
		t.Fatal("String")
	}
	if !Multi("a", "b").EqualUnordered(Multi("b", "a")) || Multi("a", "b").Equal(Multi("b", "a")) {
		t.Fatal("EqualUnordered")
	}
	if !UnorderedMulti("a", "b").Equal(Multi("b", "a")) || !Multi("b", "a").Equal(UnorderedMulti("a", "b")) {
		t.Fatal("Unordered Equal")
	}
	if Multi("a").EqualUnordered(Single("a")) || Multi("a").EqualUnordered(Multi("a", "b")) {
		t.Fatal("EqualUnordered kinds")
	}
	for k, want := range map[Kind]string{KindFlag: "flag", KindSingle: "single", KindMulti: "multi", Kind(9): "Kind(9)"} {
		if k.String() != want {
			t.Errorf("Kind %d = %s", int(k), k.String())
		}
	}
	if Flag().JSON() != nil || Single("a").JSON() != "a" || !reflect.DeepEqual(Multi("a").JSON(), []string{"a"}) {
		t.Fatal("JSON")
	}
}

// A subtree shaped like what get.json returns for a firewall ruleset.
const firewallJSON = `{
  "default-action": "drop",
  "description": "WAN to LAN",
  "rule": {
    "10": {"action": "accept", "state": {"established": "enable", "related": "enable"}},
    "20": {"action": "drop", "disable": null, "destination": {"address": ["192.0.2.0/24"]}}
  }
}`

func decode(t *testing.T, s string) any {
	t.Helper()
	var x any
	if err := json.Unmarshal([]byte(s), &x); err != nil {
		t.Fatal(err)
	}
	return Normalize(x)
}

func TestFlattenAndBuild(t *testing.T) {
	t.Parallel()
	tree := decode(t, firewallJSON)
	l := Flatten(tree)
	want := map[string]Value{
		"default-action":              Single("drop"),
		"description":                 Single("WAN to LAN"),
		"rule 10 action":              Single("accept"),
		"rule 10 state established":   Single("enable"),
		"rule 10 state related":       Single("enable"),
		"rule 20 action":              Single("drop"),
		"rule 20 disable":             Flag(),
		"rule 20 destination address": Multi("192.0.2.0/24"),
	}
	if l.Len() != len(want) {
		t.Fatalf("got %d leaves, want %d: %v", l.Len(), len(want), l.Paths())
	}
	for ps, v := range want {
		p, _ := ParsePath(ps)
		got, ok := l.Get(p)
		if !ok || !got.Equal(v) {
			t.Errorf("leaf %q = %#v (%v), want %#v", ps, got, ok, v)
		}
	}
	if !reflect.DeepEqual(Build(l), tree) {
		t.Fatalf("Build(Flatten(x)) != x\n got %#v\nwant %#v", Build(l), tree)
	}

	// Under returns relative leaves.
	r10 := l.Under(NewPath("rule", "10"))
	if r10.Len() != 3 || !r10.Has(NewPath("state", "related")) {
		t.Fatalf("Under: %v", r10.Paths())
	}
	if !l.HasUnder(NewPath("rule")) || l.HasUnder(NewPath("nope")) {
		t.Fatal("HasUnder")
	}
}

func TestFlattenEdgeCases(t *testing.T) {
	t.Parallel()
	// A bare leaf flattens to the empty path and builds back to itself.
	l := Flatten("router1")
	if v, ok := l.Get(Path{}); !ok || !v.Equal(Single("router1")) {
		t.Fatalf("leaf root: %#v", v)
	}
	if Build(l) != "router1" {
		t.Fatalf("Build = %#v", Build(l))
	}
	// Empty interior nodes keep their presence.
	l = Flatten(map[string]any{"unms": map[string]any{}})
	if v, ok := l.Get(NewPath("unms")); !ok || v.Kind != KindFlag {
		t.Fatal("empty map should be a flag")
	}
	// Empty root has no leaves.
	if Flatten(map[string]any{}).Len() != 0 {
		t.Fatal("empty root")
	}
}

func TestLookupAndChildren(t *testing.T) {
	t.Parallel()
	tree := decode(t, firewallJSON)
	if v, ok := Lookup(tree, NewPath("rule", "10", "action")); !ok || v != "accept" {
		t.Fatalf("Lookup = %v %v", v, ok)
	}
	if _, ok := Lookup(tree, NewPath("rule", "30")); ok {
		t.Fatal("missing node found")
	}
	if _, ok := Lookup(tree, NewPath("description", "x")); ok {
		t.Fatal("walked into a leaf")
	}
	if v, ok := Lookup(tree, NewPath("rule", "20", "disable")); !ok || v != nil {
		t.Fatal("flag lookup")
	}
	if got := Children(mustLookup(t, tree, "rule")); !reflect.DeepEqual(got, []string{"10", "20"}) {
		t.Fatalf("Children = %v", got)
	}
	if Children("x") != nil {
		t.Fatal("Children of leaf")
	}
}

func mustLookup(t *testing.T, tree any, names ...string) any {
	t.Helper()
	v, ok := Lookup(tree, NewPath(names...))
	if !ok {
		t.Fatalf("missing %v", names)
	}
	return v
}

func TestSetInDeleteIn(t *testing.T) {
	t.Parallel()
	tree := map[string]any{"firewall": map[string]any{}}
	SetIn(tree, NewPath("firewall", "name", "A", "rule", "10", "action"), Single("accept"))
	SetIn(tree, NewPath("firewall", "name", "A", "rule", "10", "disable"), Flag())
	SetIn(tree, NewPath("interfaces", "ethernet", "eth0", "address"), Multi("192.0.2.1/24"))
	SetIn(tree, NewPath("interfaces", "ethernet", "eth0", "address"), Multi("192.0.2.1/24", "198.51.100.1/24"))
	// A flag must not wipe a subtree.
	SetIn(tree, NewPath("firewall", "name", "A"), Flag())

	if v, _ := Lookup(tree, NewPath("interfaces", "ethernet", "eth0", "address")); !reflect.DeepEqual(v, []string{"192.0.2.1/24", "198.51.100.1/24"}) {
		t.Fatalf("multi append: %#v", v)
	}
	if _, ok := Lookup(tree, NewPath("firewall", "name", "A", "rule", "10", "action")); !ok {
		t.Fatal("flag wiped subtree")
	}

	if DeleteIn(tree, NewPath("firewall", "name", "B")) {
		t.Fatal("deleted a missing node")
	}
	if DeleteIn(tree, Path{}) {
		t.Fatal("deleted root")
	}
	if DeleteIn(tree, NewPath("firewall", "name", "A", "rule", "10", "action", "x")) {
		t.Fatal("walked through a leaf")
	}
	if !DeleteIn(tree, NewPath("firewall", "name", "A", "rule", "10", "action")) {
		t.Fatal("delete failed")
	}
	if !DeleteIn(tree, NewPath("firewall", "name", "A", "rule", "10", "disable")) {
		t.Fatal("delete failed")
	}
	// Empty parents are pruned, the top level node stays.
	fw, ok := tree["firewall"].(map[string]any)
	if !ok || len(fw) != 0 {
		t.Fatalf("pruning: %#v", tree["firewall"])
	}
}

func TestLeavesEqual(t *testing.T) {
	t.Parallel()
	a := NewLeaves()
	a.SetSingle(NewPath("x"), "1")
	b := NewLeaves()
	b.SetSingle(NewPath("x"), "1")
	if !a.Equal(b) {
		t.Fatal("equal")
	}
	b.SetSingle(NewPath("x"), "2")
	if a.Equal(b) {
		t.Fatal("different value")
	}
	b.SetSingle(NewPath("y"), "2")
	if a.Equal(b) {
		t.Fatal("different length")
	}
	b.Delete(NewPath("x"))
	b.Delete(NewPath("y"))
	b.SetSingle(NewPath("z"), "1")
	if a.Equal(b) {
		t.Fatal("different key")
	}
}

func leaves(kv ...any) Leaves {
	l := NewLeaves()
	for i := 0; i < len(kv); i += 2 {
		p, err := ParsePath(kv[i].(string))
		if err != nil {
			panic(err)
		}
		l.Set(p, kv[i+1].(Value))
	}
	return l
}

func TestDiff(t *testing.T) {
	t.Parallel()
	base := NewPath("firewall", "name", "WAN_IN")
	cases := []struct {
		name    string
		old     Leaves
		new     Leaves
		wantCLI string
		want    string
	}{
		{
			name:    "create",
			old:     NewLeaves(),
			new:     leaves("default-action", Single("drop"), "description", Single("wan")),
			wantCLI: "set firewall name WAN_IN default-action drop\nset firewall name WAN_IN description wan",
			want:    `{"SET":{"firewall":{"name":{"WAN_IN":{"default-action":"drop","description":"wan"}}}}}`,
		},
		{
			name:    "no change",
			old:     leaves("default-action", Single("drop")),
			new:     leaves("default-action", Single("drop")),
			wantCLI: "",
			want:    `{}`,
		},
		{
			name:    "change single value is a plain set",
			old:     leaves("default-action", Single("drop")),
			new:     leaves("default-action", Single("accept")),
			wantCLI: "set firewall name WAN_IN default-action accept",
			want:    `{"SET":{"firewall":{"name":{"WAN_IN":{"default-action":"accept"}}}}}`,
		},
		{
			name:    "removed leaf is deleted",
			old:     leaves("default-action", Single("drop"), "description", Single("wan")),
			new:     leaves("default-action", Single("drop")),
			wantCLI: "delete firewall name WAN_IN description",
			want:    `{"DELETE":{"firewall":{"name":{"WAN_IN":{"description":null}}}}}`,
		},
		{
			name:    "changed multi value is replaced",
			old:     leaves("address", Multi("192.0.2.1/24")),
			new:     leaves("address", Multi("192.0.2.1/24", "198.51.100.1/24")),
			wantCLI: "delete firewall name WAN_IN address\nset firewall name WAN_IN address 192.0.2.1/24\nset firewall name WAN_IN address 198.51.100.1/24",
			want:    `{"DELETE":{"firewall":{"name":{"WAN_IN":{"address":null}}}},"SET":{"firewall":{"name":{"WAN_IN":{"address":["192.0.2.1/24","198.51.100.1/24"]}}}}}`,
		},
		{
			name:    "flag added",
			old:     NewLeaves(),
			new:     leaves("disable", Flag()),
			wantCLI: "set firewall name WAN_IN disable",
			want:    `{"SET":{"firewall":{"name":{"WAN_IN":{"disable":null}}}}}`,
		},
		{
			name:    "kind change deletes first",
			old:     leaves("x", Flag()),
			new:     leaves("x", Single("1")),
			wantCLI: "delete firewall name WAN_IN x\nset firewall name WAN_IN x 1",
			want:    `{"DELETE":{"firewall":{"name":{"WAN_IN":{"x":null}}}},"SET":{"firewall":{"name":{"WAN_IN":{"x":"1"}}}}}`,
		},
		{
			name:    "quoted description",
			old:     NewLeaves(),
			new:     leaves("description", Single(`it's "quoted"`)),
			wantCLI: `set firewall name WAN_IN description "it's \"quoted\""`,
			want:    `{"SET":{"firewall":{"name":{"WAN_IN":{"description":"it's \"quoted\""}}}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := Diff(base, tc.old, tc.new)
			if got := b.String(); got != tc.wantCLI {
				t.Errorf("CLI:\n got %q\nwant %q", got, tc.wantCLI)
			}
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Errorf("JSON:\n got %s\nwant %s", raw, tc.want)
			}
			if b.Empty() != (tc.wantCLI == "") {
				t.Errorf("Empty() = %v", b.Empty())
			}
		})
	}
}

func TestBatchDeleteNesting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		paths []Path
		want  string
	}{
		{"child then parent", []Path{NewPath("a", "b", "c"), NewPath("a", "b")}, `{"DELETE":{"a":{"b":null}}}`},
		{"parent then child", []Path{NewPath("a", "b"), NewPath("a", "b", "c")}, `{"DELETE":{"a":{"b":null}}}`},
		{"siblings", []Path{NewPath("a", "b"), NewPath("a", "c")}, `{"DELETE":{"a":{"b":null,"c":null}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := NewBatch()
			for _, p := range tc.paths {
				b.Delete(p)
			}
			raw, _ := json.Marshal(b)
			if string(raw) != tc.want {
				t.Fatalf("got %s want %s", raw, tc.want)
			}
		})
	}
}

func TestBatchMergeAndAccessors(t *testing.T) {
	t.Parallel()
	a := NewBatch()
	a.Set(NewPath("system", "host-name"), Single("r1"))
	// A flag set on a node that already has children is ignored in the tree.
	a.Set(NewPath("system"), Flag())
	b := NewBatch()
	b.Delete(NewPath("system", "domain-name"))
	a.Merge(b)
	if len(a.Sets()) != 2 || len(a.Deletes()) != 1 {
		t.Fatalf("Merge: %d sets %d deletes", len(a.Sets()), len(a.Deletes()))
	}
	raw, _ := json.Marshal(a)
	want := `{"DELETE":{"system":{"domain-name":null}},"SET":{"system":{"host-name":"r1"}}}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
	if a.SetTree()["system"] == nil || a.DeleteTree()["system"] == nil {
		t.Fatal("trees")
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	in := map[string]any{"a": []any{"x", float64(1)}, "b": map[string]any{"c": nil}, "d": []string{"y"}}
	got := Normalize(in)
	want := map[string]any{"a": []string{"x", "1"}, "b": map[string]any{"c": nil}, "d": []string{"y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if !reflect.DeepEqual(DeepCopy(want), want) {
		t.Fatal("DeepCopy")
	}
}

func TestValidateValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantErr string
	}{
		{"accept", ""},
		{"two words and 'single quotes'", ""},
		{"tab\tok", ""},
		{"", ""},
		{`say "hi"`, "double quote"},
		{"line\nbreak", "control character"},
		{string([]byte{0xc3}), "UTF-8"},
	}
	for _, tc := range cases {
		err := ValidateValue(tc.in)
		if tc.wantErr == "" && err != nil {
			t.Errorf("ValidateValue(%q) = %v", tc.in, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("ValidateValue(%q) = %v, want %q", tc.in, err, tc.wantErr)
		}
	}
}
