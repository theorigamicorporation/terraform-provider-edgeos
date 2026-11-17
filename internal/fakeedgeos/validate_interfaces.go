// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"fmt"
	"net/netip"
	"regexp"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Checks for ethernet interfaces and the basic system settings.

var hostNameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func init() {
	enums = append(enums,
		enumRule{"system login user * level", []string{"admin", "operator"}},
	)
	ranges = append(ranges,
		rangeRule{"interfaces ethernet * dhcp-options default-route-distance", 1, 255, "Must be between 1-255"},
		rangeRule{"interfaces ethernet * vif * dhcp-options default-route-distance", 1, 255, "Must be between 1-255"},
	)
	setChecks = append(setChecks, checkInterfaceAddress, checkSystemValues)
	commitChecks = append(commitChecks, checkDuplexSpeed)
}

func checkInterfaceAddress(p configtree.Path, v configtree.Value) string {
	if !matchAny(p, "interfaces ethernet * address", "interfaces ethernet * vif * address") {
		return ""
	}
	for _, a := range v.Values {
		if a == "dhcp" || a == "dhcpv6" {
			continue
		}
		if _, err := netip.ParsePrefix(a); err != nil {
			return fmt.Sprintf("Invalid value [%s]: must be an IP address with prefix length, dhcp or dhcpv6", a)
		}
	}
	return ""
}

func checkSystemValues(p configtree.Path, v configtree.Value) string {
	switch {
	case match(p, "system name-server"):
		for _, a := range v.Values {
			if _, err := netip.ParseAddr(a); err != nil {
				return fmt.Sprintf("%q is not a valid IPv4 or IPv6 address", a)
			}
		}
	case match(p, "system host-name"):
		if !hostNameRE.MatchString(v.String()) {
			return "invalid host name " + v.String()
		}
	}
	return ""
}

// checkDuplexSpeed mirrors the EdgeOS rule that duplex and speed are either
// both auto or both fixed.
func checkDuplexSpeed(tree map[string]any) []commitIssue {
	eths, _ := configtree.Lookup(tree, configtree.NewPath("interfaces", "ethernet"))
	var issues []commitIssue
	for _, eth := range configtree.Children(eths) {
		duplex, _ := configtree.Lookup(eths, configtree.NewPath(eth, "duplex"))
		speed, _ := configtree.Lookup(eths, configtree.NewPath(eth, "speed"))
		d, _ := duplex.(string)
		s, _ := speed.(string)
		if (d == "" || d == "auto") != (s == "" || s == "auto") {
			issues = append(issues, commitIssue{
				Path: "interfaces ethernet " + eth,
				Msg:  "If speed is hardcoded, duplex must also be hardcoded, and the other way round",
			})
		}
	}
	return issues
}
