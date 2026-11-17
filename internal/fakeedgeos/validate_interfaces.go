// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"fmt"
	"net/netip"
	"regexp"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Checks for ethernet and switch interfaces and the basic system settings.

var (
	hostNameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	switchRE   = regexp.MustCompile(`^switch[0-9]+$`)
)

func init() {
	multiNodes = append(multiNodes,
		"interfaces switch * dhcp-options client-option",
		"interfaces switch * dhcp-options global-option",
		"interfaces switch * vif * dhcp-options client-option",
		"interfaces switch * vif * dhcp-options global-option",
	)
	enums = append(enums,
		enumRule{"system login user * level", []string{"admin", "operator"}},
		enumRule{"interfaces switch * dhcp-options default-route", []string{"update", "no-update"}},
		enumRule{"interfaces switch * dhcp-options name-server", []string{"update", "no-update"}},
		enumRule{"interfaces switch * switch-port vlan-aware", []string{"enable", "disable"}},
	)
	ranges = append(ranges,
		rangeRule{"interfaces ethernet * dhcp-options default-route-distance", 1, 255, "Must be between 1-255"},
		rangeRule{"interfaces ethernet * vif * dhcp-options default-route-distance", 1, 255, "Must be between 1-255"},
		rangeRule{"interfaces switch * dhcp-options default-route-distance", 1, 255, "Must be between 1-255"},
		rangeRule{"interfaces switch * mtu", 68, 9000, "MTU must be between 68 and 9000"},
	)
	setChecks = append(setChecks, checkInterfaceAddress, checkSwitchValues, checkSystemValues)
	commitChecks = append(commitChecks, checkDuplexSpeed, checkSwitchPorts)
}

func checkInterfaceAddress(p configtree.Path, v configtree.Value) string {
	if !matchAny(p, "interfaces ethernet * address", "interfaces ethernet * vif * address",
		"interfaces switch * address", "interfaces switch * vif * address") {
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

func checkSwitchValues(p configtree.Path, v configtree.Value) string {
	if len(p) < 3 || p[0] != "interfaces" || p[1] != "switch" {
		return ""
	}
	switch {
	case !switchRE.MatchString(p[2]):
		return fmt.Sprintf("%s is not a valid switch interface name", p[2])
	case len(p) >= 6 && p[3] == "switch-port" && p[4] == "interface" && !ethRE.MatchString(p[5]):
		return fmt.Sprintf("%s is not a valid ethernet interface name", p[5])
	case match(p, "interfaces switch * mac") && !macRE.MatchString(v.String()):
		return fmt.Sprintf("%q is not a valid MAC address", v.String())
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

// checkSwitchPorts mirrors EdgeOS refusing switch ports that do not exist or
// that are in more than one switch.
func checkSwitchPorts(tree map[string]any) []commitIssue {
	eths, _ := configtree.Lookup(tree, configtree.NewPath("interfaces", "ethernet"))
	switches, _ := configtree.Lookup(tree, configtree.NewPath("interfaces", "switch"))
	var issues []commitIssue
	owner := map[string]string{}
	for _, sw := range configtree.Children(switches) {
		ports, _ := configtree.Lookup(switches, configtree.NewPath(sw, "switch-port", "interface"))
		for _, port := range configtree.Children(ports) {
			path := "interfaces switch " + sw + " switch-port interface " + port
			if _, ok := configtree.Lookup(eths, configtree.NewPath(port)); !ok {
				issues = append(issues, commitIssue{Path: path, Msg: fmt.Sprintf("Interface %s does not exist", port)})
				continue
			}
			if other, taken := owner[port]; taken {
				issues = append(issues, commitIssue{Path: path, Msg: fmt.Sprintf("Interface %s is already a port of %s", port, other)})
				continue
			}
			owner[port] = sw
		}
	}
	return issues
}
