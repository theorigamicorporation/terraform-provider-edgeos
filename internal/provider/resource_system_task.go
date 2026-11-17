// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	taskNameRE    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	intervalRE    = regexp.MustCompile(`^([0-9]+)([mhd]?)$`)
	cronFieldRE   = regexp.MustCompile(`^[0-9A-Za-z*/,-]+$`)
	executableRE  = regexp.MustCompile(`^/\S+$`)
	cronShortcuts = []string{"@reboot", "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly"}
)

// checkTaskInterval returns why s is not an interval EdgeOS accepts, or "".
// A number without a unit is minutes; the limits are what cron can express.
func checkTaskInterval(s string) string {
	m := intervalRE.FindStringSubmatch(s)
	if m == nil {
		return "must be a number of minutes, hours or days such as 5m, 1h or 1d"
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return "must be at least 1"
	}
	unit := m[2]
	if unit == "" {
		unit = "m"
	}
	limit := map[string]int{"m": 60, "h": 24, "d": 32}[unit]
	if n >= limit {
		name := map[string]string{"m": "minutes", "h": "hours", "d": "days"}[unit]
		return fmt.Sprintf("in %s must be less than %d", name, limit)
	}
	return ""
}

// checkCrontabSpec returns why s is not a cron schedule, or "".
func checkCrontabSpec(s string) string {
	if strings.HasPrefix(s, "@") {
		if !contains(cronShortcuts, s) {
			return "must be one of " + strings.Join(cronShortcuts, ", ") + " or five cron fields"
		}
		return ""
	}
	fields := strings.Fields(s)
	if len(fields) != 5 || strings.Join(fields, " ") != s {
		return "must be five cron fields separated by single spaces, such as \"*/15 * * * *\""
	}
	for _, f := range fields {
		if !cronFieldRE.MatchString(f) {
			return fmt.Sprintf("field %q may only use digits, names, '*', '/', ',' and '-'", f)
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

// funcValidator wraps a check function that returns "" for valid strings.
type funcValidator struct {
	desc  string
	title string
	check func(string) string
}

func (v funcValidator) Description(context.Context) string { return v.desc }

func (v funcValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v funcValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	if msg := v.check(req.ConfigValue.ValueString()); msg != "" {
		resp.Diagnostics.AddAttributeError(req.Path, v.title, fmt.Sprintf("%q %s.", req.ConfigValue.ValueString(), msg))
	}
}

type systemTaskModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Interval    types.String `tfsdk:"interval"`
	CrontabSpec types.String `tfsdk:"crontab_spec"`
	Executable  types.String `tfsdk:"executable"`
	Arguments   types.String `tfsdk:"arguments"`
}

func newSystemTaskResource() resourceFactory {
	return newLeafResource(&leafSpec[systemTaskModel]{
		Name: "system_task_scheduler_task",
		Schema: schema.Schema{
			MarkdownDescription: "A scheduled task (`system task-scheduler task <name>`), a cron job that runs an " +
				"executable on the router. Set exactly one of `interval` and `crontab_spec`.\n\n" +
				"The provider does not upload the executable; put it in `/config/scripts/` (which survives firmware " +
				"upgrades) some other way. Destroying the resource deletes the task, not the executable.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The task name."),
				"name": schema.StringAttribute{
					MarkdownDescription: "Task name: letters, digits, `-` and `_`, up to 64 characters. Changing it creates a new task.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.RegexMatches(taskNameRE, "must be 1 to 64 letters, digits, '-' or '_'")},
					PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"interval": schema.StringAttribute{
					MarkdownDescription: "How often the task runs: a number followed by `m` (minutes, the default unit), " +
						"`h` or `d`, such as `5m`, `1h` or `1d`. Minutes must be below 60, hours below 24 and days below 32; " +
						"use `1h` rather than `60m`.",
					Optional: true,
					Validators: []validator.String{
						funcValidator{desc: "must be an interval such as 5m, 1h or 1d", title: "Invalid interval", check: checkTaskInterval},
						stringvalidator.ExactlyOneOf(path.MatchRoot("crontab_spec")),
					},
				},
				"crontab_spec": schema.StringAttribute{
					MarkdownDescription: "Cron schedule (`crontab-spec`): five fields such as `30 3 * * *`, or a shortcut such as `@daily`.",
					Optional:            true,
					Validators: []validator.String{
						edgeosValue(),
						funcValidator{desc: "must be a cron schedule", title: "Invalid crontab_spec", check: checkCrontabSpec},
					},
				},
				"executable": schema.StringAttribute{
					MarkdownDescription: "Absolute path of the program to run (`executable path`), such as `/config/scripts/backup.sh`.",
					Required:            true,
					Validators: []validator.String{
						edgeosValue(),
						stringvalidator.RegexMatches(executableRE, "must be an absolute path without spaces"),
					},
				},
				"arguments": schema.StringAttribute{
					MarkdownDescription: "Arguments passed to the executable (`executable arguments`), as one string. " +
						"EdgeOS values cannot contain double quotes.",
					Optional:   true,
					Validators: []validator.String{edgeosValue(), stringvalidator.LengthAtLeast(1)},
				},
			},
		},
		Path: func(m *systemTaskModel) configtree.Path {
			return configtree.NewPath("system", "task-scheduler", "task", m.Name.ValueString())
		},
		ID:    func(m *systemTaskModel) string { return m.Name.ValueString() },
		SetID: func(m *systemTaskModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(_ context.Context, m *systemTaskModel, _ *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putString(l, m.Interval, "interval")
			putString(l, m.CrontabSpec, "crontab-spec")
			putString(l, m.Executable, "executable", "path")
			putString(l, m.Arguments, "executable", "arguments")
			return l
		},
		FromTree: func(_ context.Context, tree any, m *systemTaskModel, _ *diag.Diagnostics) {
			m.Interval = getString(tree, "interval")
			m.CrontabSpec = getString(tree, "crontab-spec")
			m.Executable = getString(tree, "executable", "path")
			m.Arguments = getString(tree, "executable", "arguments")
		},
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if !taskNameRE.MatchString(id) {
				return nil, fmt.Errorf("%q is not a valid task name", id)
			}
			return map[string]attr.Value{"name": types.StringValue(id)}, nil
		},
		ImportHint: "the task name, for example ddns",
	})
}
