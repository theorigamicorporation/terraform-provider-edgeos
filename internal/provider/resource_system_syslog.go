// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	syslogFacilities = []string{
		"all", "auth", "authpriv", "cron", "daemon", "kern", "lpr", "mail", "mark", "news",
		"protocols", "security", "syslog", "user", "uucp",
		"local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7",
	}
	syslogLevels = []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}
	syslogHostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
)

const (
	systemSyslogID = "syslog"
	maxU32         = 4294967295
)

// facilitiesAttribute is the facility => level map every syslog target has.
func facilitiesAttribute(required bool, extra string) schema.MapAttribute {
	return schema.MapAttribute{
		MarkdownDescription: "Facilities to log and the lowest level logged for each (`facility <name> level <level>`), " +
			"such as `{ all = \"notice\", protocols = \"debug\" }`. Facilities: `" + strings.Join(syslogFacilities, "`, `") +
			"`. Levels: `" + strings.Join(syslogLevels, "`, `") + "`. The resource owns the whole list: facilities " +
			"that are not in the map are removed." + extra,
		Required:    required,
		Optional:    !required,
		ElementType: types.StringType,
		Validators: []validator.Map{
			mapvalidator.SizeAtLeast(1),
			mapvalidator.KeysAre(stringvalidator.OneOf(syslogFacilities...)),
			mapvalidator.ValueStringsAre(stringvalidator.OneOf(syslogLevels...)),
		},
	}
}

func archiveAttributes(attrs map[string]schema.Attribute, what string) {
	attrs["archive_files"] = schema.Int64Attribute{
		MarkdownDescription: fmt.Sprintf("Number of rotated %s files to keep (`archive files`). Unset leaves it alone.", what),
		Optional:            true,
		Validators:          []validator.Int64{int64validator.Between(1, maxU32)},
	}
	attrs["archive_size"] = schema.Int64Attribute{
		MarkdownDescription: fmt.Sprintf("Size in kilobytes at which the %s file is rotated (`archive size`). Unset leaves it alone.", what),
		Optional:            true,
		Validators:          []validator.Int64{int64validator.Between(1, maxU32)},
	}
}

func putFacilities(ctx context.Context, l configtree.Leaves, v types.Map, diags *diag.Diagnostics) {
	if !known(v) {
		return
	}
	var m map[string]string
	diags.Append(v.ElementsAs(ctx, &m, false)...)
	for f, level := range m {
		l.SetSingle(configtree.NewPath("facility", f, "level"), level)
	}
}

// getFacilities reads the facility => level map, null when there is none. A
// facility without a level reads as an empty level so the plan sets it.
func getFacilities(tree any) types.Map {
	v, ok := lookup(tree, "facility")
	if !ok {
		return types.MapNull(types.StringType)
	}
	elems := map[string]attr.Value{}
	for _, f := range configtree.Children(v) {
		level := getString(v, f, "level")
		if level.IsNull() {
			level = types.StringValue("")
		}
		elems[f] = level
	}
	return types.MapValueMust(types.StringType, elems)
}

func facilitiesOwned(v types.Map) []configtree.Path {
	if known(v) {
		return []configtree.Path{configtree.NewPath("facility", "*")}
	}
	return nil
}

// system syslog global

type systemSyslogModel struct {
	ID           types.String `tfsdk:"id"`
	Facilities   types.Map    `tfsdk:"facilities"`
	ArchiveFiles types.Int64  `tfsdk:"archive_files"`
	ArchiveSize  types.Int64  `tfsdk:"archive_size"`
}

func systemSyslogSchema() schema.Schema {
	attrs := map[string]schema.Attribute{
		"id":         idAttribute("Always `syslog`."),
		"facilities": facilitiesAttribute(false, " EdgeOS ships with `{ all = \"notice\", protocols = \"debug\" }`."),
	}
	archiveAttributes(attrs, "/var/log/messages")
	return schema.Schema{
		MarkdownDescription: "Local logging to `/var/log/messages` (`system syslog global`). There is one per router; " +
			"the resource takes over the existing settings.\n\n" +
			"Remote syslog servers are separate resources (`edgeos_system_syslog_host`), and so are extra log files " +
			"(`edgeos_system_syslog_file`), the same way `edgeos_firewall_rule` is separate from " +
			"`edgeos_firewall_name`. This resource never touches them, nor the `console` and `user` targets.\n\n" +
			"Only the attributes you set are managed. Removing an attribute from the configuration removes that " +
			"setting from the router. Import reads every attribute.\n\n" +
			"Destroying the resource deletes the settings it manages. Without any global facility the router no " +
			"longer writes `/var/log/messages`, so keep the resource with the EdgeOS defaults rather than " +
			"destroying it if you want to stop managing it.",
		Attributes: attrs,
	}
}

func systemSyslogToLeaves(ctx context.Context, m *systemSyslogModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putFacilities(ctx, l, m.Facilities, diags)
	putInt(l, m.ArchiveFiles, "archive", "files")
	putInt(l, m.ArchiveSize, "archive", "size")
	return l
}

func systemSyslogFromTree(_ context.Context, tree any, m *systemSyslogModel, diags *diag.Diagnostics) {
	// The ID is null only right after an import, which reads everything.
	all := m.ID.IsNull()
	if all || !m.Facilities.IsNull() {
		m.Facilities = getFacilities(tree)
	}
	if all || !m.ArchiveFiles.IsNull() {
		m.ArchiveFiles = getInt(tree, diags, "archive", "files")
	}
	if all || !m.ArchiveSize.IsNull() {
		m.ArchiveSize = getInt(tree, diags, "archive", "size")
	}
}

func newSystemSyslogResource() resourceFactory {
	return newLeafResource(&leafSpec[systemSyslogModel]{
		Name:        "system_syslog",
		Schema:      systemSyslogSchema(),
		Path:        func(*systemSyslogModel) configtree.Path { return configtree.NewPath("system", "syslog", "global") },
		ID:          func(*systemSyslogModel) string { return systemSyslogID },
		SetID:       func(m *systemSyslogModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    systemSyslogToLeaves,
		FromTree:    systemSyslogFromTree,
		ImportAttrs: importReadingAll(systemSyslogID),
		ImportHint:  `"syslog"`,
		Adopt:       true,
		Owned:       func(m *systemSyslogModel) []configtree.Path { return facilitiesOwned(m.Facilities) },
		Validate: func(_ context.Context, m *systemSyslogModel, diags *diag.Diagnostics) {
			if allNull(m.Facilities, m.ArchiveFiles, m.ArchiveSize) {
				diags.AddError("Nothing to manage", "Set at least one of facilities, archive_files or archive_size.")
			}
		},
	})
}

// system syslog host

type systemSyslogHostModel struct {
	ID         types.String `tfsdk:"id"`
	Host       types.String `tfsdk:"host"`
	Facilities types.Map    `tfsdk:"facilities"`
}

// syslogHostValidator accepts an IP address or a host name, each optionally
// followed by :port (IPv6 with a port as [address]:port).
type syslogHostValidator struct{}

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

func (syslogHostValidator) Description(context.Context) string {
	return "must be a host name or IP address, optionally with :port"
}

func (v syslogHostValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v syslogHostValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if known(req.ConfigValue) && !validSyslogHost(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid syslog host",
			fmt.Sprintf("%q %s, such as 192.0.2.10 or syslog.example.net:5140.", req.ConfigValue.ValueString(), v.Description(ctx)))
	}
}

func newSystemSyslogHostResource() resourceFactory {
	return newLeafResource(&leafSpec[systemSyslogHostModel]{
		Name: "system_syslog_host",
		Schema: schema.Schema{
			MarkdownDescription: "A remote syslog server (`system syslog host <host>`). EdgeOS sends the messages over UDP, " +
				"to port 514 unless the host names another port.\n\n" +
				"Destroying the resource deletes the whole host node.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("The host, as in `host`."),
				"host": schema.StringAttribute{
					MarkdownDescription: "Address or host name of the server, optionally with a port: `192.0.2.10`, " +
						"`syslog.example.net:5140`, `2001:db8::10` or `[2001:db8::10]:5140`. Changing it creates a new host.",
					Required:      true,
					Validators:    []validator.String{syslogHostValidator{}},
					PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"facilities": facilitiesAttribute(true, ""),
			},
		},
		Path: func(m *systemSyslogHostModel) configtree.Path {
			return configtree.NewPath("system", "syslog", "host", m.Host.ValueString())
		},
		ID:    func(m *systemSyslogHostModel) string { return m.Host.ValueString() },
		SetID: func(m *systemSyslogHostModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(ctx context.Context, m *systemSyslogHostModel, diags *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putFacilities(ctx, l, m.Facilities, diags)
			return l
		},
		FromTree: func(_ context.Context, tree any, m *systemSyslogHostModel, _ *diag.Diagnostics) {
			m.Facilities = getFacilities(tree)
		},
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if !validSyslogHost(id) {
				return nil, fmt.Errorf("%q is not a host name or IP address", id)
			}
			return map[string]attr.Value{"host": types.StringValue(id)}, nil
		},
		ImportHint: "the host, for example 192.0.2.10 or syslog.example.net:5140",
		Owned:      func(m *systemSyslogHostModel) []configtree.Path { return facilitiesOwned(m.Facilities) },
	})
}

// system syslog file

type systemSyslogFileModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Facilities   types.Map    `tfsdk:"facilities"`
	ArchiveFiles types.Int64  `tfsdk:"archive_files"`
	ArchiveSize  types.Int64  `tfsdk:"archive_size"`
}

func newSystemSyslogFileResource() resourceFactory {
	attrs := map[string]schema.Attribute{
		"id": idAttribute("The file name."),
		"name": schema.StringAttribute{
			MarkdownDescription: "File name below `/var/log/user/`, letters, digits, `-`, `_` and `.`. Changing it creates a new file target.",
			Required:            true,
			Validators:          []validator.String{tagName(), stringvalidator.LengthAtMost(64)},
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"facilities": facilitiesAttribute(true, ""),
	}
	archiveAttributes(attrs, "log")
	return newLeafResource(&leafSpec[systemSyslogFileModel]{
		Name: "system_syslog_file",
		Schema: schema.Schema{
			MarkdownDescription: "An extra log file (`system syslog file <name>`), written to `/var/log/user/<name>`.\n\n" +
				"Destroying the resource deletes the whole file node; EdgeOS leaves the file itself on disk.",
			Attributes: attrs,
		},
		Path: func(m *systemSyslogFileModel) configtree.Path {
			return configtree.NewPath("system", "syslog", "file", m.Name.ValueString())
		},
		ID:    func(m *systemSyslogFileModel) string { return m.Name.ValueString() },
		SetID: func(m *systemSyslogFileModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: func(ctx context.Context, m *systemSyslogFileModel, diags *diag.Diagnostics) configtree.Leaves {
			l := configtree.NewLeaves()
			putFacilities(ctx, l, m.Facilities, diags)
			putInt(l, m.ArchiveFiles, "archive", "files")
			putInt(l, m.ArchiveSize, "archive", "size")
			return l
		},
		FromTree: func(_ context.Context, tree any, m *systemSyslogFileModel, diags *diag.Diagnostics) {
			m.Facilities = getFacilities(tree)
			m.ArchiveFiles = getInt(tree, diags, "archive", "files")
			m.ArchiveSize = getInt(tree, diags, "archive", "size")
		},
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			if !tagNameRE.MatchString(id) {
				return nil, fmt.Errorf("%q is not a valid file name", id)
			}
			return map[string]attr.Value{"name": types.StringValue(id)}, nil
		},
		ImportHint: "the file name, for example firewall",
		Owned:      func(m *systemSyslogFileModel) []configtree.Path { return facilitiesOwned(m.Facilities) },
	})
}
