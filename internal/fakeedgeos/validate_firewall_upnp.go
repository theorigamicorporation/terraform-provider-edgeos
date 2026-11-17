// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Validation for the global firewall options and service upnp2, modelled on
// the EdgeOS 3.0 templates (enable/disable leaves, the source-validation
// enum, the upnp2 acl rule numbers) and on what the miniupnpd config
// generator needs at commit.

func init() {
	multiNodes = append(multiNodes, "service upnp2 listen-on")
	for _, n := range []string{
		"all-ping", "broadcast-ping", "ip-src-route", "ipv6-src-route", "ipv6-receive-redirects",
		"log-martians", "receive-redirects", "send-redirects", "syn-cookies",
	} {
		enums = append(enums, enumRule{"firewall " + n, []string{"enable", "disable"}})
	}
	enums = append(enums,
		enumRule{"firewall source-validation", []string{"strict", "loose", "disable"}},
		enumRule{"service upnp2 nat-pmp", []string{"enable", "disable"}},
		enumRule{"service upnp2 secure-mode", []string{"enable", "disable"}},
		enumRule{"service upnp2 acl rule * action", []string{"allow", "deny"}},
	)
	setChecks = append(setChecks, checkUPnPSet)
	commitChecks = append(commitChecks, checkUPnPCommit)
}

var ifNameRE = regexp.MustCompile(`^[a-z]+[0-9]+(\.[0-9]+)?$`)

// checkPortRange accepts a port or a range lo-hi from 0 to 65535.
func checkPortRange(s string) string {
	lo, hi, isRange := strings.Cut(s, "-")
	if !isRange {
		hi = lo
	}
	a, err1 := strconv.Atoi(lo)
	b, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || a < 0 || b > 65535 || a > b {
		return fmt.Sprintf("%q is not a valid port or port range", s)
	}
	return ""
}

func checkUPnPSet(p configtree.Path, v configtree.Value) string {
	if len(p) < 3 || p[0] != "service" || p[1] != "upnp2" {
		return ""
	}
	switch {
	case matchAny(p, "service upnp2 listen-on", "service upnp2 wan"):
		for _, val := range v.Values {
			if !ifNameRE.MatchString(val) {
				return fmt.Sprintf("%q is not a valid interface name", val)
			}
		}
	case len(p) >= 5 && p[2] == "acl" && p[3] == "rule":
		if n, err := strconv.Atoi(p[4]); err != nil || n < 1 || n > 9999 || strconv.Itoa(n) != p[4] {
			return "Rule number must be between 1 and 9999"
		}
		switch {
		case match(p, "service upnp2 acl rule * subnet"):
			return checkPrefix(v.String())
		case matchAny(p, "service upnp2 acl rule * external-port", "service upnp2 acl rule * local-port"):
			return checkPortRange(v.String())
		}
	}
	return ""
}

// checkUPnPCommit refuses an upnp2 node miniupnpd could not start with.
func checkUPnPCommit(tree map[string]any) []commitIssue {
	upnp, ok := configtree.Lookup(tree, configtree.NewPath("service", "upnp2"))
	if !ok {
		return nil
	}
	var issues []commitIssue
	for _, req := range []string{"listen-on", "wan"} {
		if !natHas(upnp, req) {
			issues = append(issues, commitIssue{Path: "service upnp2", Msg: req + " must be set"})
		}
	}
	rules, _ := configtree.Lookup(upnp, configtree.NewPath("acl", "rule"))
	for _, r := range configtree.Children(rules) {
		if !natHas(rules, r, "action") {
			issues = append(issues, commitIssue{Path: "service upnp2 acl rule " + r, Msg: "action must be set"})
		}
	}
	return issues
}
