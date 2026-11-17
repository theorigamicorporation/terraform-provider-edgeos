// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var (
	hostNameRE   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	domainNameRE = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	timeZoneRE   = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)
	ntpServerRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]*$`)
)

// addrValidator checks a plain IPv4 or IPv6 address.
type addrValidator struct{}

func ipAddress() validator.String { return addrValidator{} }

func (addrValidator) Description(context.Context) string { return "must be an IPv4 or IPv6 address" }

func (v addrValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (addrValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !known(req.ConfigValue) {
		return
	}
	if _, err := netip.ParseAddr(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP address",
			fmt.Sprintf("%q is not an IPv4 or IPv6 address.", req.ConfigValue.ValueString()))
	}
}

type systemModel struct {
	ID          types.String `tfsdk:"id"`
	HostName    types.String `tfsdk:"host_name"`
	DomainName  types.String `tfsdk:"domain_name"`
	TimeZone    types.String `tfsdk:"time_zone"`
	NameServers types.List   `tfsdk:"name_servers"`
	NTPServers  types.Set    `tfsdk:"ntp_servers"`
}

const systemID = "system"

func systemSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "Basic system settings (`system`). There is one per router; the resource takes over the " +
			"existing settings.\n\n" +
			"Only the attributes you set are managed. Anything you leave out, and the rest of `system` (login users, " +
			"syslog, offload, task scheduler, ...), is neither read nor changed. Removing an attribute from the " +
			"configuration removes that setting from the router. Import reads every attribute.\n\n" +
			"Destroying the resource deletes the settings it manages and leaves everything else in `system` alone. " +
			"EdgeOS then uses its defaults: the host name goes back to `ubnt`, the time zone to UTC, and the router " +
			"has no NTP servers or static name servers left.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Always `system`."),
			"host_name": schema.StringAttribute{
				MarkdownDescription: "Host name of the router, a single DNS label such as `gw01`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(hostNameRE, "must be a host name of up to 63 letters, digits and '-'")},
			},
			"domain_name": schema.StringAttribute{
				MarkdownDescription: "DNS domain of the router, such as `example.net`.",
				Optional:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(253),
					stringvalidator.RegexMatches(domainNameRE, "must be a domain name such as example.net"),
				},
			},
			"time_zone": schema.StringAttribute{
				MarkdownDescription: "Time zone name from the tz database, such as `UTC` or `Europe/Amsterdam`.",
				Optional:            true,
				Validators: []validator.String{
					edgeosValue(),
					stringvalidator.RegexMatches(timeZoneRE, "must be a time zone name such as UTC or Europe/Amsterdam"),
				},
			},
			"name_servers": schema.ListAttribute{
				MarkdownDescription: "DNS servers the router itself uses, in order of preference.",
				Optional:            true,
				ElementType:         types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(ipAddress()),
				},
			},
			"ntp_servers": schema.SetAttribute{
				MarkdownDescription: "NTP servers (`system ntp server <name>`), host names or addresses. The resource owns the " +
					"whole list: servers that are not in it are removed, including their options. Options such as `prefer` " +
					"on a server that stays in the list are kept.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(ntpServerRE, "must be a host name or IP address")),
				},
			},
		},
	}
}

func systemToLeaves(ctx context.Context, m *systemModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putString(l, m.HostName, "host-name")
	putString(l, m.DomainName, "domain-name")
	putString(l, m.TimeZone, "time-zone")
	putStrings(ctx, l, m.NameServers, diags, "name-server")
	putTags(ctx, l, m.NTPServers, diags, "ntp", "server")
	return l
}

func systemFromTree(_ context.Context, tree any, m *systemModel, _ *diag.Diagnostics) {
	// The ID is null only right after an import, which reads everything.
	all := m.ID.IsNull()
	want := func(v attr.Value) bool { return all || !v.IsNull() }
	if want(m.HostName) {
		m.HostName = getString(tree, "host-name")
	}
	if want(m.DomainName) {
		m.DomainName = getString(tree, "domain-name")
	}
	if want(m.TimeZone) {
		m.TimeZone = getString(tree, "time-zone")
	}
	if want(m.NameServers) {
		m.NameServers = getList(tree, "name-server")
	}
	if want(m.NTPServers) {
		m.NTPServers = getTags(tree, "ntp", "server")
	}
}

func systemOwned(m *systemModel) []configtree.Path {
	if known(m.NTPServers) {
		return []configtree.Path{configtree.NewPath("ntp", "server", "*")}
	}
	return nil
}

func systemImport(id string) (map[string]attr.Value, error) {
	if id != systemID {
		return nil, fmt.Errorf("the only valid ID is %q", systemID)
	}
	// Setting the ID to null marks the state as imported, so the read that
	// follows picks up every attribute.
	return map[string]attr.Value{"id": types.StringNull()}, nil
}

func newSystemResource() resourceFactory {
	return newLeafResource(&leafSpec[systemModel]{
		Name:        "system",
		Schema:      systemSchema(),
		Path:        func(*systemModel) configtree.Path { return configtree.NewPath("system") },
		ID:          func(*systemModel) string { return systemID },
		SetID:       func(m *systemModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves:    systemToLeaves,
		FromTree:    systemFromTree,
		ImportAttrs: systemImport,
		ImportHint:  `"system"`,
		MustExist:   true,
		Owned:       systemOwned,
	})
}
