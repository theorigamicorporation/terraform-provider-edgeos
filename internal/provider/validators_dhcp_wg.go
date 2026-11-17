// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Validators and helpers for the DHCP server and WireGuard resources.

// wgKeyLen is the length of a base64 encoded Curve25519 key.
const wgKeyLen = 44

// parseWGKey decodes a base64 WireGuard key (private, public or preshared).
func parseWGKey(s string) ([]byte, error) {
	if len(s) != wgKeyLen {
		return nil, fmt.Errorf("a WireGuard key is %d base64 characters, got %d", wgKeyLen, len(s))
	}
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("not a base64 encoded 32 byte key")
	}
	return b, nil
}

func isWGKey(s string) bool {
	_, err := parseWGKey(s)
	return err == nil
}

// keyFileRE matches an absolute file path without spaces or quotes, such as
// /config/auth/wg0.key.
var keyFileRE = regexp.MustCompile(`^/[A-Za-z0-9._+@-]+(/[A-Za-z0-9._+@-]+)*$`)

// isKeyFile reports whether s is an absolute path to a key file.
func isKeyFile(s string) bool {
	if !keyFileRE.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// wgPublicKey derives the public key from an inline base64 private key. It
// returns false when s is not an inline key (for example a file path).
func wgPublicKey(s string) (string, bool) {
	b, err := parseWGKey(s)
	if err != nil {
		return "", false
	}
	priv, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), true
}

// wgKeyValidator checks a WireGuard key. With files, an absolute path to a
// key file on the router is accepted as well.
type wgKeyValidator struct{ files bool }

func wireguardKey() validator.String       { return wgKeyValidator{} }
func wireguardKeyOrFile() validator.String { return wgKeyValidator{files: true} }
func (v wgKeyValidator) Description(context.Context) string {
	if v.files {
		return "must be a base64 WireGuard key (44 characters) or an absolute path to a key file on the router, such as /config/auth/wg0.key"
	}
	return "must be a base64 WireGuard key (44 characters)"
}

func (v wgKeyValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v wgKeyValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()
	// Base64 keys can start with '/', so a value that decodes to a key wins.
	if _, err := parseWGKey(s); err == nil {
		return
	}
	if v.files && strings.HasPrefix(s, "/") {
		if !isKeyFile(s) {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid key file path",
				fmt.Sprintf("%q is not a usable absolute path. Use letters, digits and '.', '_', '-' only, for example /config/auth/wg0.key.", s))
		}
		return
	}
	if _, err := parseWGKey(s); err != nil {
		// Never echo the value: it may be a secret.
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid WireGuard key", fmt.Sprintf("The value %s: %s.", v.Description(ctx), err))
	}
}

// endpointValidator checks host:port, where host is a name, an IPv4 address
// or an IPv6 address in brackets.
type endpointValidator struct{}

func wireguardEndpoint() validator.String { return endpointValidator{} }

func (endpointValidator) Description(context.Context) string {
	return "must be host:port, such as vpn.example.com:51820, 203.0.113.10:51820 or [2001:db8::1]:51820"
}

func (v endpointValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v endpointValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()
	if err := checkEndpoint(s); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid endpoint", fmt.Sprintf("%q %s: %s.", s, v.Description(ctx), err))
	}
}

var hostnameRE = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$`)

func checkEndpoint(s string) error {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return fmt.Errorf("cannot split host and port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port %q is not between 1 and 65535", port)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Is6() && !strings.HasPrefix(s, "[") {
			return fmt.Errorf("IPv6 addresses need brackets")
		}
		if a.Zone() != "" {
			return fmt.Errorf("zoned IPv6 addresses are not supported")
		}
		return nil
	}
	if strings.HasPrefix(s, "[") {
		return fmt.Errorf("%q is not an IPv6 address", host)
	}
	if len(host) > 253 || !hostnameRE.MatchString(host) {
		return fmt.Errorf("%q is not a valid host name", host)
	}
	return nil
}

// ipv4Range checks a DHCP range: both ends inside the subnet, start <= stop.
func ipv4Range(subnet netip.Prefix, start, stop string) error {
	a, err := netip.ParseAddr(start)
	if err != nil || !a.Is4() {
		return fmt.Errorf("start %q is not an IPv4 address", start)
	}
	b, err := netip.ParseAddr(stop)
	if err != nil || !b.Is4() {
		return fmt.Errorf("stop %q is not an IPv4 address", stop)
	}
	if !subnet.Contains(a) || !subnet.Contains(b) {
		return fmt.Errorf("range %s to %s is not inside %s", start, stop, subnet)
	}
	if b.Less(a) {
		return fmt.Errorf("stop %s is lower than start %s", stop, start)
	}
	return nil
}
