// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestValidateSetSystem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		val  configtree.Value
		want string // substring of the error, "" for accepted
	}{
		{"system offload hwnat", configtree.Single("enable"), ""},
		{"system offload hwnat", configtree.Single("on"), "not one of"},
		{"system offload ipv4 forwarding", configtree.Single("disable"), ""},
		{"system offload ipv4 gre", configtree.Single("enable"), ""},
		{"system offload ipv6 bonding", configtree.Single("enable"), ""},
		{"system offload ipv6 gre", configtree.Single("enable"), "is not valid"},
		{"system offload turbo", configtree.Single("enable"), "is not valid"},
		{"system offload ipv4 table-size", configtree.Single("65536"), ""},
		{"system syslog global facility all level", configtree.Single("notice"), ""},
		{"system syslog global facility all level", configtree.Single("verbose"), "not one of"},
		{"system syslog global facility everything level", configtree.Single("info"), "not a valid facility"},
		{"system syslog global archive files", configtree.Single("x"), "Number of files"},
		{"system syslog host 192.0.2.10 facility local7 level", configtree.Single("debug"), ""},
		{"system syslog host syslog.example.net:5140 facility all level", configtree.Single("info"), ""},
		{"system syslog host [2001:db8::10]:5140 facility all level", configtree.Single("info"), ""},
		{"system syslog host -bad facility all level", configtree.Single("info"), "not a valid host"},
		{"system syslog host syslog.example.net:99999 facility all level", configtree.Single("info"), "not a valid host"},
		{"system syslog host 192.0.2.10 facility bogus level", configtree.Single("info"), "not a valid facility"},
		{"system syslog file fw facility kern level", configtree.Single("warning"), ""},
		{"system syslog file ../fw facility kern level", configtree.Single("warning"), "not a valid file name"},
		{"system syslog console facility all level", configtree.Single("loud"), "not one of"},
		{"system task-scheduler task t interval", configtree.Single("5m"), ""},
		{"system task-scheduler task t interval", configtree.Single("90"), ""}, // range is a commit check
		{"system task-scheduler task t interval", configtree.Single("5s"), "Invalid interval"},
		{"system analytics-handler send-analytics-report", configtree.Single("false"), ""},
		{"system crash-handler send-crash-report", configtree.Single("no"), "not one of"},
	}
	for _, tc := range cases {
		got := validateSet(mustPath(t, tc.path), tc.val)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTaskSchedulerCommitChecks(t *testing.T) {
	t.Parallel()
	task := func(m map[string]any) map[string]any {
		return map[string]any{"system": map[string]any{"task-scheduler": map[string]any{"task": map[string]any{"t": m}}}}
	}
	exe := map[string]any{"path": "/config/scripts/t.sh"}
	cases := []struct {
		task map[string]any
		want string
	}{
		{map[string]any{"interval": "5m", "executable": exe}, ""},
		{map[string]any{"interval": "23h", "executable": exe}, ""},
		{map[string]any{"crontab-spec": "*/5 * * * *", "executable": exe}, ""},
		{map[string]any{"executable": exe}, "must define either interval or crontab-spec"},
		{map[string]any{"interval": "5m", "crontab-spec": "@daily", "executable": exe}, "at the same time"},
		{map[string]any{"interval": "5m"}, "must define executable path"},
		{map[string]any{"interval": "60", "executable": exe}, "minutes must be less than 60"},
		{map[string]any{"interval": "24h", "executable": exe}, "hours must be less than 24"},
		{map[string]any{"interval": "32d", "executable": exe}, "days must be less than 32"},
		{map[string]any{"interval": "0m", "executable": exe}, "positive number"},
	}
	for _, tc := range cases {
		got := issuesFor(task(tc.task))
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%v: got %q, want %q", tc.task, got, tc.want)
		}
	}
}
