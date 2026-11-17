// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"regexp"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Validation for service ssh, service gui and the public keys of login
// users, modelled on the EdgeOS 3.0.1 templates (port u32 ranges, ipv4/ipv6
// listen addresses, the key type list) and on what sshd and lighttpd report
// when they cannot start.

func init() {
	multiNodes = append(multiNodes,
		"service ssh listen-address",
		"service gui listen-address",
	)
	enums = append(enums,
		enumRule{"service ssh protocol-version", []string{"v1", "v2", "all"}},
		enumRule{"service gui older-ciphers", []string{"enable", "disable"}},
		enumRule{"system login user * authentication public-keys * type", []string{
			"ssh-dss", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-ed25519",
		}},
	)
	ranges = append(ranges,
		rangeRule{"service ssh port", 1, 65535, "Port number must be in range 1 to 65535"},
		rangeRule{"service gui http-port", 1, 65535, "Port number must be in range 1 to 65535"},
		rangeRule{"service gui https-port", 1, 65535, "Port number must be in range 1 to 65535"},
	)
	setChecks = append(setChecks, checkAccessSet)
	commitChecks = append(commitChecks, checkAccessCommit)
}

var (
	base64RE  = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
	absPathRE = regexp.MustCompile(`^/[^ ]+$`)
)

func checkAccessSet(p configtree.Path, v configtree.Value) string {
	switch {
	case matchAny(p, "service ssh listen-address", "service gui listen-address"):
		for _, val := range v.Values {
			if _, err := netip.ParseAddr(val); err != nil {
				return fmt.Sprintf("%q is not a valid IPv4 or IPv6 address", val)
			}
		}
	case matchAny(p, "service gui ca-file", "service gui cert-file"):
		if !absPathRE.MatchString(v.String()) {
			return fmt.Sprintf("%q is not an absolute file name", v.String())
		}
	case match(p, "system login user * authentication public-keys * key"):
		if _, err := base64.StdEncoding.DecodeString(v.String()); err != nil || !base64RE.MatchString(v.String()) {
			return "Invalid public key: only the base64 part of the key is allowed, without type or comment"
		}
	}
	return ""
}

func checkAccessCommit(tree map[string]any) []commitIssue {
	var issues []commitIssue
	users, _ := configtree.Lookup(tree, configtree.NewPath("system", "login", "user"))
	for _, u := range configtree.Children(users) {
		keys, _ := configtree.Lookup(users, configtree.NewPath(u, "authentication", "public-keys"))
		for _, k := range configtree.Children(keys) {
			base := fmt.Sprintf("system login user %s authentication public-keys %s", u, k)
			if !natHas(keys, k, "type") {
				issues = append(issues, commitIssue{Path: base, Msg: "Must specify public key type"})
			}
			if !natHas(keys, k, "key") {
				issues = append(issues, commitIssue{Path: base, Msg: "Must specify public key value"})
			}
		}
	}

	gui, _ := configtree.Lookup(tree, configtree.NewPath("service", "gui"))
	httpPort, httpsPort := natStr(gui, "http-port"), natStr(gui, "https-port")
	if httpPort != "" && httpPort == httpsPort {
		issues = append(issues, commitIssue{Path: "service gui", Msg: "http-port and https-port must be different"})
	}
	if sshPort := natStr(tree, "service", "ssh", "port"); sshPort != "" && (sshPort == httpPort || sshPort == httpsPort) {
		issues = append(issues, commitIssue{Path: "service ssh", Msg: fmt.Sprintf("Port %s is already in use by the web UI", sshPort)})
	}
	return issues
}
