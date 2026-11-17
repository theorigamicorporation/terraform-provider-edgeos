// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// valueValidator rejects strings EdgeOS refuses in any value, so the error
// shows up at plan time instead of as a failed commit.
type valueValidator struct{}

func edgeosValue() validator.String { return valueValidator{} }

func (valueValidator) Description(context.Context) string {
	return "must be a valid EdgeOS value (no double quotes or control characters)"
}

func (v valueValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (valueValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	if err := configtree.ValidateValue(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid EdgeOS value", err.Error())
	}
}

// prefixValidator checks an IP prefix such as 192.0.2.0/24.
type prefixValidator struct {
	v4Only    bool
	canonical bool
}

func ipv4Prefix() validator.String { return prefixValidator{v4Only: true, canonical: true} }

func ipPrefix() validator.String { return prefixValidator{} }

func (p prefixValidator) Description(context.Context) string {
	if p.v4Only {
		return "must be an IPv4 network in CIDR notation, such as 192.0.2.0/24"
	}
	return "must be an IP address with prefix length, such as 192.0.2.1/24"
}

func (p prefixValidator) MarkdownDescription(ctx context.Context) string { return p.Description(ctx) }

func (p prefixValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()
	pfx, err := netip.ParsePrefix(s)
	switch {
	case err != nil:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid prefix", fmt.Sprintf("%q %s.", s, p.Description(context.Background())))
	case p.v4Only && !pfx.Addr().Is4():
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid prefix", fmt.Sprintf("%q is not IPv4.", s))
	case p.canonical && pfx.Masked() != pfx:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid prefix",
			fmt.Sprintf("%q has host bits set; EdgeOS expects the network address %s.", s, pfx.Masked()))
	}
}

// ipValidator checks a plain IP address.
type ipValidator struct{ v4Only bool }

func ipv4Address() validator.String { return ipValidator{v4Only: true} }

func (ipValidator) Description(context.Context) string { return "must be an IPv4 address" }

func (v ipValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v ipValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()
	a, err := netip.ParseAddr(s)
	if err != nil || (v.v4Only && !a.Is4()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP address", fmt.Sprintf("%q is not an IPv4 address.", s))
	}
}

var (
	firewallNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,27}$`)
	ethernetRE     = regexp.MustCompile(`^eth[0-9]+$`)
	wireguardRE    = regexp.MustCompile(`^wg[0-9]{1,3}$`)
	macRE          = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
	protocolRE     = regexp.MustCompile(`^!?[a-z0-9_]+$`)
	portSpecRE     = regexp.MustCompile(`^!?[a-z0-9-]+(,[a-z0-9-]+)*$`)
	interfaceRE    = regexp.MustCompile(`^[a-z]+[0-9]+(\.[0-9]+)?$`)
	tagNameRE      = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

func firewallName() validator.String {
	return stringvalidator.RegexMatches(firewallNameRE,
		"must be 1 to 27 characters of letters, digits, '-', '_' or '.'")
}

func ethernetName() validator.String {
	return stringvalidator.RegexMatches(ethernetRE, "must be an ethernet interface name such as eth0")
}

func wireguardName() validator.String {
	return stringvalidator.RegexMatches(wireguardRE, "must be a WireGuard interface name from wg0 to wg999")
}

func macAddress() validator.String {
	return stringvalidator.RegexMatches(macRE, "must be a lower case MAC address such as 02:00:5e:10:00:01")
}

func portSpec() validator.String {
	return stringvalidator.RegexMatches(portSpecRE,
		"must be a port, a range (8000-8080), a service name (https) or a comma separated list of those")
}

func interfaceName() validator.String {
	return stringvalidator.RegexMatches(interfaceRE, "must be an interface name such as eth0, eth0.10, switch0 or wg0")
}

func tagName() validator.String {
	return stringvalidator.RegexMatches(tagNameRE, "must only use letters, digits, '-', '_' and '.'")
}
