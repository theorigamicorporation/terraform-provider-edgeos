// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package client_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// fakeTransport records calls and detects overlapping Apply calls.
type fakeTransport struct {
	tree     map[string]any
	inFlight atomic.Int32
	overlaps atomic.Int32
	applies  atomic.Int32
	err      error
}

func (f *fakeTransport) Name() string { return "fake" }

func (f *fakeTransport) Read(_ context.Context, p configtree.Path) (any, bool, error) {
	v, ok := configtree.Lookup(f.tree, p)
	return v, ok, f.err
}

func (f *fakeTransport) Apply(_ context.Context, _ *configtree.Batch) error {
	if f.inFlight.Add(1) > 1 {
		f.overlaps.Add(1)
	}
	time.Sleep(2 * time.Millisecond)
	f.inFlight.Add(-1)
	f.applies.Add(1)
	return f.err
}

func (f *fakeTransport) SystemInfo(context.Context) (client.SystemInfo, error) {
	return client.ParseSoftwareVersion("EdgeRouter.ER-e50.v3.0.1.1.2.3"), nil
}

func TestApplySerialised(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	c := client.New(ft)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := configtree.NewBatch()
			b.Set(configtree.NewPath("system", "host-name"), configtree.Single("r"))
			if err := c.Apply(context.Background(), b); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ft.overlaps.Load() != 0 {
		t.Fatalf("%d overlapping commits", ft.overlaps.Load())
	}
	if ft.applies.Load() != 20 {
		t.Fatalf("applies = %d", ft.applies.Load())
	}
}

func TestApplyValidation(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	c := client.New(ft)
	ctx := context.Background()
	if err := c.Apply(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, configtree.NewBatch()); err != nil || ft.applies.Load() != 0 {
		t.Fatal("empty batch was sent")
	}
	cases := []struct {
		name string
		b    func() *configtree.Batch
		want string
	}{
		{"double quote", func() *configtree.Batch {
			b := configtree.NewBatch()
			b.Set(configtree.NewPath("system", "host-name"), configtree.Single(`a"b`))
			return b
		}, "double quote"},
		{"empty element in set", func() *configtree.Batch {
			b := configtree.NewBatch()
			b.Set(configtree.NewPath("system", ""), configtree.Single("x"))
			return b
		}, "invalid set path"},
		{"empty element in delete", func() *configtree.Batch {
			b := configtree.NewBatch()
			b.Delete(configtree.NewPath("", "x"))
			return b
		}, "invalid delete path"},
	}
	for _, tc := range cases {
		err := c.Apply(ctx, tc.b())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if ft.applies.Load() != 0 {
		t.Fatal("invalid batch reached the transport")
	}
}

func TestReadHelpers(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{tree: map[string]any{"system": map[string]any{"host-name": "r1", "ntp": map[string]any{"server": map[string]any{"a": nil}}}}}
	c := client.New(ft)
	ctx := context.Background()
	l, ok, err := c.ReadLeaves(ctx, configtree.NewPath("system"))
	if err != nil || !ok || l.Len() != 2 {
		t.Fatalf("ReadLeaves = %v %v %v", l.Paths(), ok, err)
	}
	if _, ok, _ := c.ReadLeaves(ctx, configtree.NewPath("nope")); ok {
		t.Fatal("missing node reported")
	}
	if ok, _ := c.Exists(ctx, configtree.NewPath("system", "host-name")); !ok {
		t.Fatal("Exists")
	}
	if _, _, err := c.Read(ctx, configtree.Path{}); err == nil {
		t.Fatal("empty path accepted")
	}
	if c.Transport().Name() != "fake" {
		t.Fatal("Transport")
	}
	info, _ := c.SystemInfo(ctx)
	if info.Platform != "e50" {
		t.Fatalf("info %+v", info)
	}
	ft.err = errors.New("boom")
	if _, _, err := c.ReadLeaves(ctx, configtree.NewPath("system")); err == nil {
		t.Fatal("error swallowed")
	}
}

func TestParseSoftwareVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in                       string
		platform, version, build string
	}{
		{"EdgeRouter.ER-e50.v3.0.1.5800000.250900.1200", "e50", "v3.0.1", "5800000.250900.1200"},
		{"EdgeRouter.ER-e300.v2.0.9-hotfix.7.5622731.230615.0857", "e300", "v2.0.9-hotfix", "7.5622731.230615.0857"},
		{"EdgeRouter.ER-e100.v1.10.11.5274249.191223.1110", "e100", "v1.10.11", "5274249.191223.1110"},
		{"something else", "", "", ""},
	}
	for _, tc := range cases {
		got := client.ParseSoftwareVersion(tc.in)
		if got.SoftwareVersion != tc.in || got.Platform != tc.platform || got.Version != tc.version || got.Build != tc.build {
			t.Errorf("ParseSoftwareVersion(%q) = %+v", tc.in, got)
		}
	}
	if client.PlatformModels["e50"] == "" {
		t.Fatal("PlatformModels")
	}
}

func TestCommitErrorMessage(t *testing.T) {
	t.Parallel()
	err := &client.CommitError{Stage: "COMMIT", Errors: []client.PathError{
		{Path: "firewall name X rule 10", Message: "Rule action [REQUIRED]"},
		{Message: "Commit failed"},
	}}
	want := "EdgeOS rejected the change (COMMIT failed)\n  firewall name X rule 10: Rule action [REQUIRED]\n  Commit failed"
	if err.Error() != want {
		t.Fatalf("got %q", err.Error())
	}
}
