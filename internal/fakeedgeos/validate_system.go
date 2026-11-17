// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Validation for system offload, system syslog, system task-scheduler and
// the analytics and crash report switches, modelled on the EdgeOS 3.0.1
// templates and the task scheduler's commit script.

func init() {
	onOff := []string{"enable", "disable"}
	levels := []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}
	enums = append(enums,
		enumRule{"system syslog global facility * level", levels},
		enumRule{"system syslog host * facility * level", levels},
		enumRule{"system syslog file * facility * level", levels},
		enumRule{"system syslog console facility * level", levels},
		enumRule{"system syslog user * facility * level", levels},
		enumRule{"system analytics-handler send-analytics-report", []string{"true", "false"}},
		enumRule{"system crash-handler send-crash-report", []string{"true", "false"}},
	)
	ranges = append(ranges,
		rangeRule{"system syslog global archive files", 0, 4294967295, "Number of files must be between 0 and 4294967295"},
		rangeRule{"system syslog global archive size", 0, 4294967295, "Size must be between 0 and 4294967295"},
		rangeRule{"system syslog file * archive files", 0, 4294967295, "Number of files must be between 0 and 4294967295"},
		rangeRule{"system syslog file * archive size", 0, 4294967295, "Size must be between 0 and 4294967295"},
	)
	for _, n := range offloadNodes {
		if !strings.HasSuffix(n, "flow-lifetime") && !strings.HasSuffix(n, "table-size") {
			enums = append(enums, enumRule{n, onOff})
		}
	}
	setChecks = append(setChecks, checkSystemSet)
	commitChecks = append(commitChecks, checkTaskSchedulerCommit)
}

// offloadNodes are the nodes below system offload in the EdgeOS 3.0.1
// templates. hwnat only does something on the MediaTek models (ER-X family),
// the ipv4 and ipv6 nodes only on the Cavium models, but the templates are
// the same on every platform.
var offloadNodes = []string{
	"system offload hwnat",
	"system offload ipsec",
	"system offload flow-lifetime",
	"system offload ipv4 forwarding",
	"system offload ipv4 vlan",
	"system offload ipv4 pppoe",
	"system offload ipv4 gre",
	"system offload ipv4 bonding",
	"system offload ipv4 table-size",
	"system offload ipv6 forwarding",
	"system offload ipv6 vlan",
	"system offload ipv6 pppoe",
	"system offload ipv6 bonding",
}

var syslogFacilities = []string{
	"all", "auth", "authpriv", "cron", "daemon", "kern", "lpr", "mail", "mark", "news",
	"protocols", "security", "syslog", "user", "uucp",
	"local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7",
}

var (
	syslogHostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
	syslogFileRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	intervalRE   = regexp.MustCompile(`^[0-9]+[mhd]?$`)
)

func checkSystemSet(p configtree.Path, v configtree.Value) string {
	switch {
	case len(p) >= 3 && p[0] == "system" && p[1] == "offload":
		for _, n := range offloadNodes {
			if match(p, n) {
				return ""
			}
		}
		return fmt.Sprintf("Configuration path: [%s] is not valid", p.String())
	case len(p) >= 3 && p[0] == "system" && p[1] == "syslog":
		return checkSyslogSet(p)
	case match(p, "system task-scheduler task * interval"):
		if !intervalRE.MatchString(v.String()) {
			return "Invalid interval: must be a number, optionally followed by m, h or d"
		}
	}
	return ""
}

func checkSyslogSet(p configtree.Path) string {
	// The facility tag sits at a different depth for each target.
	var facility string
	switch {
	case len(p) >= 5 && (p[2] == "global" || p[2] == "console") && p[3] == "facility":
		facility = p[4]
	case len(p) >= 6 && (p[2] == "host" || p[2] == "file" || p[2] == "user") && p[4] == "facility":
		facility = p[5]
	}
	if facility != "" && !contains(syslogFacilities, facility) {
		return fmt.Sprintf("%q is not a valid facility name", facility)
	}
	if len(p) >= 4 && p[2] == "host" && !validSyslogHost(p[3]) {
		return fmt.Sprintf("%q is not a valid host name or IP address", p[3])
	}
	if len(p) >= 4 && p[2] == "file" && !syslogFileRE.MatchString(p[3]) {
		return fmt.Sprintf("%q is not a valid file name", p[3])
	}
	return ""
}

// validSyslogHost accepts an address or host name, optionally with :port
// (IPv6 addresses as [addr]:port).
func validSyslogHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Port() > 0
	}
	if !syslogHostRE.MatchString(s) {
		return false
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		n, err := strconv.Atoi(s[i+1:])
		return err == nil && n >= 1 && n <= 65535
	}
	return true
}

// checkTaskSchedulerCommit mirrors the checks of vyatta-update-crontab: one
// of interval and crontab-spec, an executable, and intervals cron can express.
func checkTaskSchedulerCommit(tree map[string]any) []commitIssue {
	tasks, ok := configtree.Lookup(tree, configtree.NewPath("system", "task-scheduler", "task"))
	if !ok {
		return nil
	}
	var issues []commitIssue
	for _, name := range configtree.Children(tasks) {
		task, _ := configtree.Lookup(tasks, configtree.NewPath(name))
		add := func(msg string) {
			issues = append(issues, commitIssue{Path: "system task-scheduler task " + name, Msg: msg})
		}
		interval := natStr(task, "interval")
		hasInterval, hasSpec := natHas(task, "interval"), natHas(task, "crontab-spec")
		switch {
		case hasInterval && hasSpec:
			add(fmt.Sprintf("Invalid task %q: can not use interval and crontab-spec at the same time", name))
		case !hasInterval && !hasSpec:
			add(fmt.Sprintf("Invalid task %q: must define either interval or crontab-spec", name))
		case hasInterval:
			if msg := checkInterval(interval); msg != "" {
				add(msg)
			}
		}
		if natStr(task, "executable", "path") == "" {
			add(fmt.Sprintf("Invalid task %q: must define executable path", name))
		}
	}
	return issues
}

func checkInterval(s string) string {
	unit := "m"
	if n := len(s); n > 0 && strings.ContainsRune("mhd", rune(s[n-1])) {
		unit, s = s[n-1:], s[:n-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return "Interval must be a positive number"
	}
	limit := map[string]int{"m": 60, "h": 24, "d": 32}[unit]
	name := map[string]string{"m": "minutes", "h": "hours", "d": "days"}[unit]
	if n >= limit {
		return fmt.Sprintf("Interval in %s must be less than %d", name, limit)
	}
	return ""
}
