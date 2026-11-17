// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Validation for service nat, port-forward and protocols static, modelled on
// the checks in the EdgeOS templates and the NAT commit script.

func init() {
	ranges = append(ranges,
		rangeRule{"protocols static interface-route * next-hop-interface * distance", 1, 255, "Must be between 1-255"},
	)
	setChecks = append(setChecks, checkNATSet)
	commitChecks = append(commitChecks, checkNATCommit, checkPortForwardCommit, checkStaticCommit)
}

func checkPrefix(s string) string {
	pfx, err := netip.ParsePrefix(s)
	switch {
	case err != nil || !pfx.Addr().Is4():
		return fmt.Sprintf("%q is not a valid IPv4 prefix", s)
	case pfx.Masked() != pfx:
		return fmt.Sprintf("Invalid network %s, the network address is %s", s, pfx.Masked())
	}
	return ""
}

func checkIPv4(s string) string {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return fmt.Sprintf("%q is not a valid IPv4 address", s)
	}
	return ""
}

func checkNATSet(p configtree.Path, v configtree.Value) string {
	switch {
	case len(p) >= 4 && p[0] == "protocols" && p[1] == "static" && (p[2] == "route" || p[2] == "interface-route"):
		if msg := checkPrefix(p[3]); msg != "" {
			return msg
		}
		if len(p) >= 6 && p[2] == "route" && p[4] == "next-hop" {
			return checkIPv4(p[5])
		}
	case len(p) >= 3 && p[0] == "port-forward" && p[1] == "rule":
		if n, err := strconv.ParseUint(p[2], 10, 32); err != nil || n == 0 {
			return "Rule number must be a positive 32 bit number"
		}
		if match(p, "port-forward rule * forward-to address") {
			return checkIPv4(v.String())
		}
	}
	return ""
}

func natHas(tree any, names ...string) bool {
	_, ok := configtree.Lookup(tree, configtree.NewPath(names...))
	return ok
}

func natStr(tree any, names ...string) string {
	v, _ := configtree.Lookup(tree, configtree.NewPath(names...))
	s, _ := v.(string)
	return s
}

func checkNATCommit(tree map[string]any) []commitIssue {
	rules, ok := configtree.Lookup(tree, configtree.NewPath("service", "nat", "rule"))
	if !ok {
		return nil
	}
	var issues []commitIssue
	for _, r := range configtree.Children(rules) {
		rule, _ := configtree.Lookup(rules, configtree.NewPath(r))
		add := func(msg string) { issues = append(issues, commitIssue{Path: "service nat rule " + r, Msg: msg}) }
		typ := natStr(rule, "type")
		exclude := natHas(rule, "exclude")
		switch typ {
		case "destination":
			if natHas(rule, "outbound-interface") {
				add("outbound-interface not applicable for destination rules")
			}
			if natHas(rule, "outside-address") {
				add("outside-address not applicable for destination rules")
			}
			if !exclude && !natHas(rule, "inside-address", "address") {
				add("inside-address not specified")
			}
		case "source", "masquerade":
			if natHas(rule, "inbound-interface") {
				add("inbound-interface not applicable for " + typ + " rules")
			}
			if natHas(rule, "inside-address") {
				add("inside-address not applicable for " + typ + " rules")
			}
			if typ == "source" && !exclude && !natHas(rule, "outside-address", "address") {
				add("outside-address not specified")
			}
			if typ == "masquerade" && natHas(rule, "outside-address", "address") {
				add("outside address should not be defined for masquerade rules")
			}
		}
		proto := strings.TrimPrefix(natStr(rule, "protocol"), "!")
		if proto != "tcp" && proto != "udp" && proto != "tcp_udp" {
			for _, where := range []string{"source", "destination", "inside-address", "outside-address"} {
				if natHas(rule, where, "port") {
					add("ports can only be specified when protocol is tcp, udp or tcp_udp")
					break
				}
			}
		}
	}
	return issues
}

func checkPortForwardCommit(tree map[string]any) []commitIssue {
	rules, ok := configtree.Lookup(tree, configtree.NewPath("port-forward", "rule"))
	if !ok {
		return nil
	}
	var issues []commitIssue
	for _, r := range configtree.Children(rules) {
		rule, _ := configtree.Lookup(rules, configtree.NewPath(r))
		for _, req := range [][]string{{"original-port"}, {"forward-to", "address"}} {
			if !natHas(rule, req...) {
				issues = append(issues, commitIssue{Path: "port-forward rule " + r, Msg: strings.Join(req, " ") + " must be set"})
			}
		}
	}
	return issues
}

func checkStaticCommit(tree map[string]any) []commitIssue {
	var issues []commitIssue
	routes, _ := configtree.Lookup(tree, configtree.NewPath("protocols", "static", "route"))
	for _, r := range configtree.Children(routes) {
		route, _ := configtree.Lookup(routes, configtree.NewPath(r))
		if len(configtree.Children(natChild(route, "next-hop"))) == 0 && !natHas(route, "blackhole") {
			issues = append(issues, commitIssue{Path: "protocols static route " + r, Msg: "Must define next-hop or blackhole"})
		}
	}
	ifRoutes, _ := configtree.Lookup(tree, configtree.NewPath("protocols", "static", "interface-route"))
	for _, r := range configtree.Children(ifRoutes) {
		route, _ := configtree.Lookup(ifRoutes, configtree.NewPath(r))
		if len(configtree.Children(natChild(route, "next-hop-interface"))) == 0 {
			issues = append(issues, commitIssue{Path: "protocols static interface-route " + r, Msg: "Must define next-hop-interface"})
		}
	}
	return issues
}

func natChild(tree any, name string) any {
	v, _ := configtree.Lookup(tree, configtree.NewPath(name))
	return v
}
