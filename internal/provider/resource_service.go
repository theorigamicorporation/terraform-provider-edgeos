// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

var filePathRE = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

func portAttribute(desc string, def int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: fmt.Sprintf("%s Defaults to `%d`.", desc, def),
		Optional:            true,
		Computed:            true,
		Default:             int64default.StaticInt64(def),
		Validators:          []validator.Int64{int64validator.Between(1, 65535)},
	}
}

func flagAttribute(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: desc + " Defaults to `false`.",
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
	}
}

func listenAddressesAttribute(what string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: fmt.Sprintf("Local IPv4 or IPv6 addresses %s listens on (`listen-address`). Unset listens on all addresses.", what),
		Optional:            true,
		ElementType:         types.StringType,
		Validators: []validator.Set{
			setvalidator.SizeAtLeast(1),
			setvalidator.ValueStringsAre(ipAddress()),
		},
	}
}

// singletonImport accepts only the fixed ID of a singleton resource.
func singletonImport(id, want string) (map[string]attr.Value, error) {
	if id != want {
		return nil, fmt.Errorf("the only valid ID is %q", want)
	}
	return map[string]attr.Value{"id": types.StringValue(want)}, nil
}

// service ssh

type serviceSSHModel struct {
	ID                            types.String `tfsdk:"id"`
	Port                          types.Int64  `tfsdk:"port"`
	ListenAddresses               types.Set    `tfsdk:"listen_addresses"`
	ProtocolVersion               types.String `tfsdk:"protocol_version"`
	DisablePasswordAuthentication types.Bool   `tfsdk:"disable_password_authentication"`
	DisableHostValidation         types.Bool   `tfsdk:"disable_host_validation"`
	AllowRoot                     types.Bool   `tfsdk:"allow_root"`
}

const serviceSSHID = "ssh"

func serviceSSHSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "The SSH server (`service ssh`). There is one per router; the resource takes over the " +
			"existing settings and the ID is always `ssh`.\n\n" +
			"The resource owns every attribute below: one you leave out gets its default, so the configuration " +
			"describes the whole SSH server. Other options below `service ssh` are left alone. When `service ssh` " +
			"is missing (SSH switched off), create switches it on.\n\n" +
			"Destroying the resource never switches SSH off. It puts the port back to 22 and the protocol to `v2`, " +
			"listens on all addresses again and removes `disable-password-authentication`, `disable-host-validation` " +
			"and `allow-root`. To switch SSH off, delete `service ssh` with `edgeos_config` or by hand.\n\n" +
			"**Lockout risk.** A wrong value here can cut off SSH access to the router: " +
			"`disable_password_authentication` without a working key (see `edgeos_system_login_user_public_key`), " +
			"a `port` the firewall does not allow, or `listen_addresses` that are not on the interface you connect " +
			"through. The provider itself uses the web UI, not SSH, so it keeps working and can undo the change.",
		Attributes: map[string]schema.Attribute{
			"id":               idAttribute("Always `ssh`."),
			"port":             portAttribute("TCP port of the SSH server.", 22),
			"listen_addresses": listenAddressesAttribute("the SSH server"),
			"protocol_version": schema.StringAttribute{
				MarkdownDescription: "SSH protocol version: `v2`, `v1` or `all`. Defaults to `v2`; protocol 1 is insecure.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("v2"),
				Validators:          []validator.String{stringvalidator.OneOf("v1", "v2", "all")},
			},
			"disable_password_authentication": flagAttribute("Only allow public key logins (`disable-password-authentication`). " +
				"Add a key with `edgeos_system_login_user_public_key` first."),
			"disable_host_validation": flagAttribute("Do not look up the client's host name in DNS (`disable-host-validation`), " +
				"which makes logins faster when DNS is slow or missing."),
			"allow_root": flagAttribute("Allow logins as `root` (`allow-root`)."),
		},
	}
}

func serviceSSHToLeaves(ctx context.Context, m *serviceSSHModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putInt(l, m.Port, "port")
	putStrings(ctx, l, m.ListenAddresses, diags, "listen-address")
	putString(l, m.ProtocolVersion, "protocol-version")
	putFlag(l, m.DisablePasswordAuthentication, "disable-password-authentication")
	putFlag(l, m.DisableHostValidation, "disable-host-validation")
	putFlag(l, m.AllowRoot, "allow-root")
	return l
}

func serviceSSHFromTree(_ context.Context, tree any, m *serviceSSHModel, diags *diag.Diagnostics) {
	m.Port = getInt(tree, diags, "port")
	m.ListenAddresses = getSet(tree, "listen-address")
	m.ProtocolVersion = getString(tree, "protocol-version")
	m.DisablePasswordAuthentication = getFlag(tree, "disable-password-authentication")
	m.DisableHostValidation = getFlag(tree, "disable-host-validation")
	m.AllowRoot = getFlag(tree, "allow-root")
}

func serviceSSHOwned(*serviceSSHModel) []configtree.Path {
	return []configtree.Path{
		configtree.NewPath("listen-address"),
		configtree.NewPath("disable-password-authentication"),
		configtree.NewPath("disable-host-validation"),
		configtree.NewPath("allow-root"),
	}
}

func serviceSSHReset(*serviceSSHModel) configtree.Leaves {
	l := configtree.NewLeaves()
	l.SetSingle(configtree.NewPath("port"), "22")
	l.SetSingle(configtree.NewPath("protocol-version"), "v2")
	return l
}

func newServiceSSHResource() resourceFactory {
	return newLeafResource(&leafSpec[serviceSSHModel]{
		Name:     "service_ssh",
		Schema:   serviceSSHSchema(),
		Path:     func(*serviceSSHModel) configtree.Path { return configtree.NewPath("service", "ssh") },
		ID:       func(*serviceSSHModel) string { return serviceSSHID },
		SetID:    func(m *serviceSSHModel, id string) { m.ID = types.StringValue(id) },
		ToLeaves: serviceSSHToLeaves,
		FromTree: serviceSSHFromTree,
		ImportAttrs: func(id string) (map[string]attr.Value, error) {
			return singletonImport(id, serviceSSHID)
		},
		ImportHint: `"ssh"`,
		Adopt:      true,
		Owned:      serviceSSHOwned,
		Reset:      serviceSSHReset,
	})
}

// service gui

type serviceGUIModel struct {
	ID              types.String `tfsdk:"id"`
	HTTPPort        types.Int64  `tfsdk:"http_port"`
	HTTPSPort       types.Int64  `tfsdk:"https_port"`
	ListenAddresses types.Set    `tfsdk:"listen_addresses"`
	OlderCiphers    types.Bool   `tfsdk:"older_ciphers"`
	CAFile          types.String `tfsdk:"ca_file"`
	CertFile        types.String `tfsdk:"cert_file"`
}

const serviceGUIID = "gui"

func filePathAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Validators:          []validator.String{stringvalidator.RegexMatches(filePathRE, "must be an absolute path such as /config/auth/gui.pem")},
	}
}

func serviceGUISchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "The web UI (`service gui`). There is one per router; the resource takes over the existing " +
			"settings and the ID is always `gui`.\n\n" +
			"The resource owns every attribute below: one you leave out gets its default. Other options below " +
			"`service gui` are left alone. Destroying the resource puts the ports back to 80 and 443, turns " +
			"`older-ciphers` back on, listens on all addresses again and removes `ca-file` and `cert-file`.\n\n" +
			"**The provider talks to the router through this web UI.** Changing `https_port` or `listen_addresses` " +
			"moves the endpoint the provider uses while it applies the change: the commit goes through, but the " +
			"read that follows can fail, and later runs cannot reach the router until the provider's `host` and " +
			"`port` match the new settings. Change them in a run of their own, then update the provider " +
			"configuration. Make sure the firewall allows the new port first, or the only way back is SSH or the " +
			"console.",
		Attributes: map[string]schema.Attribute{
			"id":               idAttribute("Always `gui`."),
			"http_port":        portAttribute("Port of the plain HTTP listener, which redirects to HTTPS.", 80),
			"https_port":       portAttribute("Port of the HTTPS listener, which the provider connects to.", 443),
			"listen_addresses": listenAddressesAttribute("the web UI"),
			"older_ciphers": schema.BoolAttribute{
				MarkdownDescription: "Accept older TLS ciphers for old browsers (`older-ciphers enable`). Defaults to `true`, as on a new router; set `false` to allow only current ciphers.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"ca_file": filePathAttribute("Path of a CA certificate on the router to send along with the server certificate, such as `/config/auth/ca.pem`."),
			"cert_file": filePathAttribute("Path of the server certificate on the router, a PEM file with the certificate and its key, " +
				"such as `/config/auth/gui.pem`. Unset uses the self-signed certificate. The file has to exist before the commit."),
		},
	}
}

func serviceGUIToLeaves(ctx context.Context, m *serviceGUIModel, diags *diag.Diagnostics) configtree.Leaves {
	l := configtree.NewLeaves()
	putInt(l, m.HTTPPort, "http-port")
	putInt(l, m.HTTPSPort, "https-port")
	putStrings(ctx, l, m.ListenAddresses, diags, "listen-address")
	putEnable(l, m.OlderCiphers, "older-ciphers")
	putString(l, m.CAFile, "ca-file")
	putString(l, m.CertFile, "cert-file")
	return l
}

func serviceGUIFromTree(_ context.Context, tree any, m *serviceGUIModel, diags *diag.Diagnostics) {
	m.HTTPPort = getInt(tree, diags, "http-port")
	m.HTTPSPort = getInt(tree, diags, "https-port")
	m.ListenAddresses = getSet(tree, "listen-address")
	m.OlderCiphers = getEnable(tree, "older-ciphers")
	m.CAFile = getString(tree, "ca-file")
	m.CertFile = getString(tree, "cert-file")
}

func serviceGUIOwned(*serviceGUIModel) []configtree.Path {
	return []configtree.Path{
		configtree.NewPath("listen-address"),
		configtree.NewPath("ca-file"),
		configtree.NewPath("cert-file"),
	}
}

func serviceGUIReset(*serviceGUIModel) configtree.Leaves {
	l := configtree.NewLeaves()
	l.SetSingle(configtree.NewPath("http-port"), "80")
	l.SetSingle(configtree.NewPath("https-port"), "443")
	l.SetSingle(configtree.NewPath("older-ciphers"), "enable")
	return l
}

func validateServiceGUI(_ context.Context, m *serviceGUIModel, diags *diag.Diagnostics) {
	if m.HTTPPort.IsUnknown() || m.HTTPSPort.IsUnknown() {
		return
	}
	// The config holds null for a port left at its default.
	http, https := int64(80), int64(443)
	if !m.HTTPPort.IsNull() {
		http = m.HTTPPort.ValueInt64()
	}
	if !m.HTTPSPort.IsNull() {
		https = m.HTTPSPort.ValueInt64()
	}
	if http == https {
		diags.AddAttributeError(path.Root("https_port"), "Same port twice",
			fmt.Sprintf("http_port and https_port are both %d; the web UI needs two different ports.", http))
	}
}

var serviceGUISpec = &leafSpec[serviceGUIModel]{
	Name:     "service_gui",
	Schema:   serviceGUISchema(),
	Path:     func(*serviceGUIModel) configtree.Path { return configtree.NewPath("service", "gui") },
	ID:       func(*serviceGUIModel) string { return serviceGUIID },
	SetID:    func(m *serviceGUIModel, id string) { m.ID = types.StringValue(id) },
	ToLeaves: serviceGUIToLeaves,
	FromTree: serviceGUIFromTree,
	ImportAttrs: func(id string) (map[string]attr.Value, error) {
		return singletonImport(id, serviceGUIID)
	},
	ImportHint: `"gui"`,
	Adopt:      true,
	Owned:      serviceGUIOwned,
	Reset:      serviceGUIReset,
	Validate:   validateServiceGUI,
}

// serviceGUIResource warns at plan time when a change moves the endpoint the
// provider talks to.
type serviceGUIResource struct {
	*leafResource[serviceGUIModel]
}

var _ resource.ResourceWithModifyPlan = (*serviceGUIResource)(nil)

func newServiceGUIResource() resource.Resource {
	return &serviceGUIResource{leafResource: &leafResource[serviceGUIModel]{spec: serviceGUISpec}}
}

func (r *serviceGUIResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state serviceGUIModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if guiEndpointChanges(&state, &plan) {
		resp.Diagnostics.AddWarning("The provider's endpoint changes",
			"This plan changes https_port or listen_addresses of the web UI, which the provider connects to. "+
				"The read after the commit may fail; afterwards set the provider's host and port to the new values.")
	}
}

func guiEndpointChanges(state, plan *serviceGUIModel) bool {
	return (!plan.HTTPSPort.IsUnknown() && !plan.HTTPSPort.Equal(state.HTTPSPort)) ||
		(!plan.ListenAddresses.IsUnknown() && !plan.ListenAddresses.Equal(state.ListenAddresses))
}
