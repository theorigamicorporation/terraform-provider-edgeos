// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// match reports whether p matches pattern, where "*" matches any one element.
func match(p configtree.Path, pattern string) bool {
	parts := strings.Fields(pattern)
	if len(parts) != len(p) {
		return false
	}
	for i, part := range parts {
		if part != "*" && part != p[i] {
			return false
		}
	}
	return true
}

func matchAny(p configtree.Path, patterns ...string) bool {
	for _, pat := range patterns {
		if match(p, pat) {
			return true
		}
	}
	return false
}

// multiNodes are the multi-valued leaves the fake knows about, taken from the
// EdgeOS templates. A single string sent to one of them is stored as a list.
var multiNodes = []string{
	"interfaces ethernet * address",
	"interfaces ethernet * vif * address",
	"interfaces switch * address",
	"interfaces switch * vif * address",
	"interfaces wireguard * address",
	"interfaces wireguard * peer * allowed-ips",
	"interfaces ethernet * dhcp-options client-option",
	"interfaces ethernet * dhcp-options global-option",
	"interfaces ethernet * vif * dhcp-options client-option",
	"interfaces ethernet * vif * dhcp-options global-option",
	"system name-server",
	"port-forward lan-interface",
	"service dhcp-server global-parameters",
	"service dhcp-server shared-network-name * shared-network-parameters",
	"service dhcp-server shared-network-name * subnet * dns-server",
	"service dhcp-server shared-network-name * subnet * ntp-server",
	"service dhcp-server shared-network-name * subnet * exclude",
	"service dhcp-server shared-network-name * subnet * subnet-parameters",
	"service dhcp-server shared-network-name * subnet * static-mapping * static-mapping-parameters",
	"service dns forwarding listen-on",
	"service dns forwarding name-server",
	"service dns forwarding options",
}

func isMultiNode(p configtree.Path) bool { return matchAny(p, multiNodes...) }

var (
	ethRE  = regexp.MustCompile(`^eth[0-9]+$`)
	wgRE   = regexp.MustCompile(`^wg[0-9]+$`)
	macRE  = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)
	alnum  = regexp.MustCompile(`^[A-Za-z0-9]*$`)
	fwName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,27}$`)
)

type enumRule struct {
	pattern string
	allowed []string
}

var enums = []enumRule{
	{"firewall name * default-action", []string{"drop", "accept", "reject"}},
	{"firewall name * rule * action", []string{"drop", "reject", "reject-tcp", "accept"}},
	{"firewall name * rule * log", []string{"enable", "disable"}},
	{"firewall name * rule * state established", []string{"enable", "disable"}},
	{"firewall name * rule * state related", []string{"enable", "disable"}},
	{"firewall name * rule * state new", []string{"enable", "disable"}},
	{"firewall name * rule * state invalid", []string{"enable", "disable"}},
	{"service nat rule * type", []string{"source", "destination", "masquerade"}},
	{"service nat rule * log", []string{"enable", "disable"}},
	{"port-forward rule * protocol", []string{"tcp", "udp", "tcp_udp"}},
	{"port-forward auto-firewall", []string{"enable", "disable"}},
	{"port-forward hairpin-nat", []string{"enable", "disable"}},
	{"interfaces ethernet * duplex", []string{"auto", "half", "full"}},
	{"interfaces ethernet * speed", []string{"auto", "10", "100", "1000", "10000"}},
	{"interfaces ethernet * dhcp-options default-route", []string{"update", "no-update"}},
	{"interfaces ethernet * dhcp-options name-server", []string{"update", "no-update"}},
	{"interfaces ethernet * vif * dhcp-options default-route", []string{"update", "no-update"}},
	{"interfaces ethernet * vif * dhcp-options name-server", []string{"update", "no-update"}},
	{"interfaces wireguard * route-allowed-ips", []string{"true", "false"}},
	{"service dhcp-server shared-network-name * authoritative", []string{"enable", "disable"}},
}

type rangeRule struct {
	pattern string
	min     int
	max     int
	msg     string
}

var ranges = []rangeRule{
	{"interfaces ethernet * mtu", 68, 9000, "MTU must be between 68 and 9000"},
	{"interfaces ethernet * vif * mtu", 68, 9000, "MTU must be between 68 and 9000"},
	{"interfaces wireguard * mtu", 68, 9000, "MTU must be between 68 and 9000"},
	{"interfaces wireguard * listen-port", 1, 65535, "Port must be between 1 and 65535"},
	{"interfaces wireguard * peer * persistent-keepalive", 1, 65535, "Keepalive must be between 1 and 65535"},
	{"protocols static route * next-hop * distance", 1, 255, "Must be between 1-255"},
	{"protocols static route * blackhole distance", 1, 255, "Must be between 1-255"},
	{"service dhcp-server shared-network-name * subnet * lease", 120, 4294967295, "Lease must be at least 120 seconds"},
}

// Extra checks registered by other files of this package with init(). They
// keep the validation for one area of the config next to each other.
var (
	setChecks    []func(p configtree.Path, v configtree.Value) string
	commitChecks []func(tree map[string]any) []commitIssue
)

// validateSet returns an error message for a value EdgeOS would refuse at
// set time, or "".
func validateSet(p configtree.Path, v configtree.Value) string {
	for _, check := range setChecks {
		if msg := check(p, v); msg != "" {
			return msg
		}
	}
	for _, val := range v.Values {
		if strings.ContainsRune(val, '"') {
			return "Cannot use the double quote character in a value string"
		}
	}
	for _, name := range p {
		if strings.ContainsRune(name, '"') {
			return "Cannot use the double quote character in a value string"
		}
	}
	for _, e := range enums {
		if match(p, e.pattern) {
			for _, val := range v.Values {
				if !contains(e.allowed, val) {
					return fmt.Sprintf("Value validation failed: %q is not one of %s", val, strings.Join(e.allowed, ", "))
				}
			}
		}
	}
	for _, r := range ranges {
		if match(p, r.pattern) {
			n, err := strconv.Atoi(v.String())
			if err != nil || n < r.min || n > r.max {
				return r.msg
			}
		}
	}
	switch {
	case len(p) >= 3 && p[0] == "interfaces" && p[1] == "ethernet" && !ethRE.MatchString(p[2]):
		return fmt.Sprintf("%s is not a valid ethernet interface name", p[2])
	case len(p) >= 3 && p[0] == "interfaces" && p[1] == "wireguard" && !wgRE.MatchString(p[2]):
		return "wg must be (wg0-wg999)"
	case len(p) >= 3 && p[0] == "firewall" && p[1] == "name" && !fwName.MatchString(p[2]):
		return "Firewall name must be less than 28 characters long and only use letters, digits, - _ and ."
	case len(p) >= 5 && p[0] == "interfaces" && p[1] == "ethernet" && p[3] == "vif":
		if n, err := strconv.Atoi(p[4]); err != nil || n < 0 || n > 4094 {
			return "VLAN ID must be between 0 and 4094"
		}
	case len(p) >= 5 && p[0] == "firewall" && p[1] == "name" && p[3] == "rule":
		if n, err := strconv.Atoi(p[4]); err != nil || n < 1 || n > 9999 {
			return "firewall rule number must be between 1 and 9999"
		}
	case len(p) >= 4 && p[0] == "service" && p[1] == "nat" && p[2] == "rule":
		if n, err := strconv.Atoi(p[3]); err != nil || n < 1 || n > 9999 {
			return "Rule number must be between 1 and 9999."
		}
	case len(p) >= 4 && p[0] == "protocols" && p[1] == "static" && p[2] == "route":
		if _, err := netip.ParsePrefix(p[3]); err != nil {
			return fmt.Sprintf("%q is not a valid IPv4 prefix", p[3])
		}
	}
	if matchAny(p, "interfaces ethernet * mac", "interfaces ethernet * vif * mac",
		"service dhcp-server shared-network-name * subnet * static-mapping * mac-address") {
		if !macRE.MatchString(v.String()) {
			return fmt.Sprintf("%q is not a valid MAC address", v.String())
		}
	}
	if matchAny(p, "system login user * authentication plaintext-password") {
		// EdgeOS 3.x refuses passwords made only of letters and digits.
		if alnum.MatchString(v.String()) {
			return "Password must contain at least one non-alphanumeric character"
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// validateCommit runs the checks EdgeOS does at commit time.
func validateCommit(tree map[string]any) []commitIssue {
	var issues []commitIssue
	for _, check := range commitChecks {
		issues = append(issues, check(tree)...)
	}
	fwNames := map[string]bool{}
	if names, ok := configtree.Lookup(tree, configtree.NewPath("firewall", "name")); ok {
		for _, n := range configtree.Children(names) {
			fwNames[n] = true
			rules, _ := configtree.Lookup(names, configtree.NewPath(n, "rule"))
			for _, r := range configtree.Children(rules) {
				if _, ok := configtree.Lookup(rules, configtree.NewPath(r, "action")); !ok {
					issues = append(issues, commitIssue{
						Path: fmt.Sprintf("firewall name %s rule %s", n, r),
						Msg:  "Rule action [REQUIRED]",
					})
				}
			}
		}
	}

	// Interfaces may only reference existing firewall rule sets.
	l := configtree.Flatten(tree["interfaces"])
	for _, p := range l.Paths() {
		if len(p) < 3 || p[len(p)-1] != "name" || p[len(p)-3] != "firewall" {
			continue
		}
		v, _ := l.Get(p)
		if !fwNames[v.String()] {
			issues = append(issues, commitIssue{
				Path: "interfaces " + p.Parent().String(),
				Msg:  fmt.Sprintf("Firewall name [%s] does not exist", v.String()),
			})
		}
	}

	if rules, ok := configtree.Lookup(tree, configtree.NewPath("service", "nat", "rule")); ok {
		for _, r := range configtree.Children(rules) {
			typ, _ := configtree.Lookup(rules, configtree.NewPath(r, "type"))
			path := "service nat rule " + r
			switch typ {
			case nil:
				issues = append(issues, commitIssue{Path: path, Msg: "Rule type must be set"})
			case "destination":
				if _, ok := configtree.Lookup(rules, configtree.NewPath(r, "inbound-interface")); !ok {
					issues = append(issues, commitIssue{Path: path, Msg: "inbound-interface not specified"})
				}
			default:
				if _, ok := configtree.Lookup(rules, configtree.NewPath(r, "outbound-interface")); !ok {
					issues = append(issues, commitIssue{Path: path, Msg: "outbound-interface not specified"})
				}
			}
		}
	}

	if pf, ok := tree["port-forward"].(map[string]any); ok {
		if _, hasRules := pf["rule"]; hasRules {
			if _, ok := pf["wan-interface"]; !ok {
				issues = append(issues, commitIssue{Path: "port-forward", Msg: "wan-interface must be set"})
			}
			if _, ok := pf["lan-interface"]; !ok {
				issues = append(issues, commitIssue{Path: "port-forward", Msg: "lan-interface must be set"})
			}
		}
	}

	if wgs, ok := configtree.Lookup(tree, configtree.NewPath("interfaces", "wireguard")); ok {
		for _, wg := range configtree.Children(wgs) {
			if _, ok := configtree.Lookup(wgs, configtree.NewPath(wg, "private-key")); !ok {
				issues = append(issues, commitIssue{Path: "interfaces wireguard " + wg, Msg: "private-key must be set"})
			}
			peers, _ := configtree.Lookup(wgs, configtree.NewPath(wg, "peer"))
			for _, p := range configtree.Children(peers) {
				if _, ok := configtree.Lookup(peers, configtree.NewPath(p, "allowed-ips")); !ok {
					issues = append(issues, commitIssue{Path: "interfaces wireguard " + wg + " peer " + p, Msg: "allowed-ips must be set"})
				}
			}
		}
	}

	if nets, ok := configtree.Lookup(tree, configtree.NewPath("service", "dhcp-server", "shared-network-name")); ok {
		for _, n := range configtree.Children(nets) {
			subnets, _ := configtree.Lookup(nets, configtree.NewPath(n, "subnet"))
			for _, sn := range configtree.Children(subnets) {
				maps, _ := configtree.Lookup(subnets, configtree.NewPath(sn, "static-mapping"))
				for _, m := range configtree.Children(maps) {
					base := fmt.Sprintf("service dhcp-server shared-network-name %s subnet %s static-mapping %s", n, sn, m)
					for _, req := range []string{"ip-address", "mac-address"} {
						if _, ok := configtree.Lookup(maps, configtree.NewPath(m, req)); !ok {
							issues = append(issues, commitIssue{Path: base, Msg: req + " must be set"})
						}
					}
				}
			}
		}
	}
	return issues
}

// postCommit mimics what EdgeOS does to the config during commit: plaintext
// passwords are hashed and blanked.
func postCommit(tree map[string]any) {
	users, ok := configtree.Lookup(tree, configtree.NewPath("system", "login", "user"))
	if !ok {
		return
	}
	for _, u := range configtree.Children(users) {
		auth, ok := configtree.Lookup(users, configtree.NewPath(u, "authentication"))
		m, isMap := auth.(map[string]any)
		if !ok || !isMap {
			continue
		}
		if pw, _ := m["plaintext-password"].(string); pw != "" {
			m["encrypted-password"] = HashPassword(pw)
			m["plaintext-password"] = ""
		}
	}
}

func sortPaths(ps []configtree.Path) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].String() < ps[j].String() })
}
