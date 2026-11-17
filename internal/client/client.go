// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Package client is the provider's view of an EdgeOS device. It hides the
// transport behind a small interface and serialises writes.
package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Transport talks to one EdgeOS device.
type Transport interface {
	// Read returns the subtree at path. The boolean is false when the node
	// does not exist.
	Read(ctx context.Context, path configtree.Path) (any, bool, error)
	// Apply runs all DELETE then SET operations of the batch and commits
	// them as one transaction. A failed commit leaves the device unchanged.
	Apply(ctx context.Context, b *configtree.Batch) error
	// SystemInfo returns the firmware version string of the device.
	SystemInfo(ctx context.Context) (SystemInfo, error)
	// Name identifies the transport in logs and diagnostics.
	Name() string
}

// SystemInfo describes the running firmware.
type SystemInfo struct {
	// SoftwareVersion is the raw version string, for example
	// "EdgeRouter.ER-e50.v3.0.1.5862409.250924.1407".
	SoftwareVersion string
	// Platform is the hardware platform code such as "e50".
	Platform string
	// Version is the firmware release such as "v3.0.1".
	Version string
	// Build is the build identifier after the version.
	Build string
}

var swVerRE = regexp.MustCompile(`^[^.]+\.ER-([a-z0-9]+)\.(v\d+\.\d+\.\d+[^.]*)\.(.+)$`)

// ParseSoftwareVersion splits an EdgeOS sw_ver string into its parts. Strings
// it does not understand are kept as SoftwareVersion only.
func ParseSoftwareVersion(s string) SystemInfo {
	info := SystemInfo{SoftwareVersion: s}
	if m := swVerRE.FindStringSubmatch(s); m != nil {
		info.Platform, info.Version, info.Build = m[1], m[2], m[3]
	}
	return info
}

// PlatformModels maps EdgeOS platform codes to the models that share them.
var PlatformModels = map[string]string{
	"e50":   "EdgeRouter X, X SFP, 10X",
	"e100":  "EdgeRouter Lite, PoE",
	"e200":  "EdgeRouter 8, Pro",
	"e300":  "EdgeRouter 4, 6P, 12, 12P",
	"e1000": "EdgeRouter Infinity",
}

// Client wraps a Transport. Writes are serialised because EdgeOS config
// sessions fail when two commits overlap.
type Client struct {
	t  Transport
	mu sync.Mutex
}

// New returns a client for the given transport.
func New(t Transport) *Client {
	return &Client{t: t}
}

// Transport returns the underlying transport.
func (c *Client) Transport() Transport { return c.t }

// Read returns the subtree at path.
func (c *Client) Read(ctx context.Context, path configtree.Path) (any, bool, error) {
	if err := path.Validate(); err != nil {
		return nil, false, err
	}
	return c.t.Read(ctx, path)
}

// ReadLeaves returns the leaves below path, relative to it.
func (c *Client) ReadLeaves(ctx context.Context, path configtree.Path) (configtree.Leaves, bool, error) {
	x, ok, err := c.Read(ctx, path)
	if err != nil || !ok {
		return configtree.NewLeaves(), ok, err
	}
	return configtree.Flatten(x), true, nil
}

// Exists reports whether the node at path exists.
func (c *Client) Exists(ctx context.Context, path configtree.Path) (bool, error) {
	_, ok, err := c.Read(ctx, path)
	return ok, err
}

// Apply commits the batch. Empty batches are not sent.
func (c *Client) Apply(ctx context.Context, b *configtree.Batch) error {
	if b == nil || b.Empty() {
		return nil
	}
	for _, p := range b.Deletes() {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("invalid delete path %q: %w", p.String(), err)
		}
	}
	for _, op := range b.Sets() {
		if err := op.Path.Validate(); err != nil {
			return fmt.Errorf("invalid set path %q: %w", op.Path.String(), err)
		}
		for _, v := range op.Value.Values {
			if err := configtree.ValidateValue(v); err != nil {
				return fmt.Errorf("invalid value for %q: %w", op.Path.String(), err)
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t.Apply(ctx, b)
}

// SystemInfo returns the firmware details.
func (c *Client) SystemInfo(ctx context.Context) (SystemInfo, error) {
	return c.t.SystemInfo(ctx)
}

// ErrAuth is returned when the device rejects the credentials.
var ErrAuth = errors.New("authentication failed")

// PathError is one message reported by EdgeOS, optionally tied to a path.
type PathError struct {
	Path    string
	Message string
}

// CommitError is returned when EdgeOS refuses a batch. Stage is the step that
// failed: SET, DELETE, COMMIT or SAVE.
type CommitError struct {
	Stage  string
	Errors []PathError
}

func (e *CommitError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "EdgeOS rejected the change (%s failed)", e.Stage)
	for _, pe := range e.Errors {
		b.WriteString("\n  ")
		if pe.Path != "" {
			b.WriteString(pe.Path)
			b.WriteString(": ")
		}
		b.WriteString(pe.Message)
	}
	return b.String()
}
