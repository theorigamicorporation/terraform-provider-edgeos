// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package configtree

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzPathRoundTrip checks that any valid path survives String and
// ParsePath unchanged. The input is split on NUL into path elements.
func FuzzPathRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"firewall\x00name\x00WAN_IN",
		"description\x00two words",
		`a"b` + "\x00" + `c\d`,
		"it's\x00\"quoted\"",
		"tab\there\x00new\nline",
		"\x00",
		"café\x00日本",
		"192.0.2.0/24\x00aGVsbG8+/w==",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p := Path(strings.Split(in, "\x00"))
		if p.Validate() != nil {
			// Control characters other than tab/newline, empty elements and
			// invalid UTF-8 are rejected before they get here; still make
			// sure rendering does not panic.
			_ = p.String()
			return
		}
		s := p.String()
		back, err := ParsePath(s)
		if err != nil {
			t.Fatalf("ParsePath(%q) for %#v: %v", s, p, err)
		}
		if !back.Equal(p) {
			t.Fatalf("round trip %#v -> %q -> %#v", p, s, back)
		}
	})
}

// FuzzParsePath feeds arbitrary text to the parser. It must never panic, and
// anything it accepts must render and parse back to the same path.
func FuzzParsePath(f *testing.F) {
	for _, seed := range []string{
		"system host-name",
		`a "b c" 'd e'`,
		`"unterminated`,
		`'x`,
		`"\q"`,
		`  `,
		`"a\"b"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p, err := ParsePath(in)
		if err != nil {
			return
		}
		back, err := ParsePath(p.String())
		if err != nil {
			t.Fatalf("re-parse of %q (from %q): %v", p.String(), in, err)
		}
		if !back.Equal(p) {
			t.Fatalf("%q -> %#v -> %q -> %#v", in, p, p.String(), back)
		}
	})
}

// FuzzBatchJSON checks that a single SET survives the JSON encoding used by
// batch.json, whatever the path and value contain.
func FuzzBatchJSON(f *testing.F) {
	f.Add("system\x00host-name", "router-1")
	f.Add("interfaces\x00ethernet\x00eth0\x00description", `quote " and \ backslash`)
	f.Add("a", "<script>&amp;")
	f.Fuzz(func(t *testing.T, rawPath, value string) {
		p := Path(strings.Split(rawPath, "\x00"))
		// Terraform strings are always valid UTF-8.
		if p.Validate() != nil || !utf8.ValidString(value) {
			return
		}
		b := NewBatch()
		b.Set(p, Single(value))
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			SET map[string]any `json:"SET"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		got, ok := Lookup(body.SET, p)
		if !ok {
			t.Fatalf("path %#v missing in %s", p, raw)
		}
		// encoding/json replaces invalid UTF-8 with U+FFFD, so compare
		// through the same lossy conversion.
		want := strings.ToValidUTF8(value, "�")
		if got != want {
			t.Fatalf("value %q came back as %q", want, got)
		}
	})
}

// FuzzValidateValue checks that ValidateValue never panics and that anything
// it accepts is free of the characters EdgeOS refuses.
func FuzzValidateValue(f *testing.F) {
	for _, s := range []string{"ok", `q"`, "a\nb", "tab\t", "\x7f"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateValue(s) != nil {
			return
		}
		if strings.ContainsAny(s, "\"\n\r\x00") || !utf8.ValidString(s) {
			t.Fatalf("accepted %q", s)
		}
	})
}
