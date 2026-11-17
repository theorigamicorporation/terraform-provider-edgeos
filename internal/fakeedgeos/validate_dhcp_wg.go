// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Validation for service dhcp-server and interfaces wireguard, modelled on
// the EdgeOS templates (node types ipv4, ipv4net, u32 and the WireGuard
// package's key checks) and on the errors dhcpd and wg report at commit.

func init() {
	multiNodes = append(multiNodes,
		"service dhcp-server shared-network-name * subnet * wins-server",
		"service dhcp-server shared-network-name * subnet * time-server",
	)
	enums = append(enums,
		enumRule{"service dhcp-server disabled", []string{"true", "false"}},
		enumRule{"service dhcp-server use-dnsmasq", []string{"enable", "disable"}},
		enumRule{"service dhcp-server hostfile-update", []string{"enable", "disable"}},
		enumRule{"service dhcp-server static-arp", []string{"enable", "disable"}},
	)
	ranges = append(ranges,
		rangeRule{"interfaces wireguard * fwmark", 0, 4294967295, "fwmark must be between 0 and 4294967295"},
	)
	setChecks = append(setChecks, checkDHCPSet, checkWireguardSet)
	commitChecks = append(commitChecks, checkDHCPCommit)
}

const dhcpBase = "service dhcp-server shared-network-name * subnet *"

func isIPv4(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is4()
}

func checkDHCPSet(p configtree.Path, v configtree.Value) string {
	if !match(p[:min(len(p), 6)], dhcpBase) {
		return ""
	}
	if pfx, err := netip.ParsePrefix(p[5]); err != nil || !pfx.Addr().Is4() || pfx.Masked() != pfx {
		return fmt.Sprintf("%q is not a valid IPv4 network address", p[5])
	}
	ipv4Leaves := []string{
		dhcpBase + " default-router", dhcpBase + " dns-server", dhcpBase + " ntp-server",
		dhcpBase + " time-server", dhcpBase + " wins-server", dhcpBase + " exclude",
		dhcpBase + " unifi-controller", dhcpBase + " start * stop",
		dhcpBase + " static-mapping * ip-address",
	}
	if matchAny(p, ipv4Leaves...) {
		for _, val := range v.Values {
			if !isIPv4(val) {
				return fmt.Sprintf("%q is not a valid IPv4 address", val)
			}
		}
	}
	if len(p) >= 8 && p[6] == "start" && !isIPv4(p[7]) {
		return fmt.Sprintf("%q is not a valid IPv4 address", p[7])
	}
	return ""
}

func isKey(s string) bool {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == 32
}

func checkWireguardSet(p configtree.Path, v configtree.Value) string {
	switch {
	case matchAny(p, "interfaces wireguard * private-key", "interfaces wireguard * peer * preshared-key"):
		s := v.String()
		if !isKey(s) && !strings.HasPrefix(s, "/") {
			return "Key must be a base64 encoded key or an absolute path to a key file"
		}
	case len(p) >= 5 && p[0] == "interfaces" && p[1] == "wireguard" && p[3] == "peer":
		if !isKey(p[4]) {
			return fmt.Sprintf("%q is not a valid WireGuard public key", p[4])
		}
		if match(p, "interfaces wireguard * peer * endpoint") {
			host, port, err := net.SplitHostPort(v.String())
			n, perr := strconv.Atoi(port)
			if err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
				return "Endpoint must be host:port"
			}
		}
		if match(p, "interfaces wireguard * peer * allowed-ips") {
			for _, val := range v.Values {
				if _, err := netip.ParsePrefix(val); err != nil {
					return fmt.Sprintf("%q is not a valid IP prefix", val)
				}
			}
		}
	case match(p, "interfaces wireguard * address"):
		for _, val := range v.Values {
			if _, err := netip.ParsePrefix(val); err != nil {
				return fmt.Sprintf("%q is not a valid IP address with prefix length", val)
			}
		}
	}
	return ""
}

// checkDHCPCommit runs the checks dhcpd and the EdgeOS commit scripts do:
// pools and static mappings must be inside their subnet.
func checkDHCPCommit(tree map[string]any) []commitIssue {
	var issues []commitIssue
	nets, _ := configtree.Lookup(tree, configtree.NewPath("service", "dhcp-server", "shared-network-name"))
	for _, n := range configtree.Children(nets) {
		subnets, _ := configtree.Lookup(nets, configtree.NewPath(n, "subnet"))
		for _, sn := range configtree.Children(subnets) {
			pfx, err := netip.ParsePrefix(sn)
			if err != nil {
				continue
			}
			base := fmt.Sprintf("service dhcp-server shared-network-name %s subnet %s", n, sn)
			starts, _ := configtree.Lookup(subnets, configtree.NewPath(sn, "start"))
			for _, start := range configtree.Children(starts) {
				stop, ok := configtree.Lookup(starts, configtree.NewPath(start, "stop"))
				s, _ := stop.(string)
				if !ok || s == "" {
					issues = append(issues, commitIssue{Path: base + " start " + start, Msg: "stop must be set"})
					continue
				}
				a, b := netip.MustParseAddr(start), netip.MustParseAddr(s)
				if !pfx.Contains(a) || !pfx.Contains(b) || b.Less(a) {
					issues = append(issues, commitIssue{Path: base + " start " + start,
						Msg: fmt.Sprintf("DHCP range %s-%s is not inside subnet %s", start, s, sn)})
				}
			}
			maps, _ := configtree.Lookup(subnets, configtree.NewPath(sn, "static-mapping"))
			for _, m := range configtree.Children(maps) {
				ip, _ := configtree.Lookup(maps, configtree.NewPath(m, "ip-address"))
				if s, ok := ip.(string); ok && isIPv4(s) && !pfx.Contains(netip.MustParseAddr(s)) {
					issues = append(issues, commitIssue{Path: base + " static-mapping " + m,
						Msg: fmt.Sprintf("Static mapping address %s is not inside subnet %s", s, sn)})
				}
			}
		}
	}
	return issues
}
