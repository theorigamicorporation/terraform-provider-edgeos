// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Package provider implements the EdgeOS Terraform and OpenTofu provider.
package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client/webapi"
)

var _ provider.Provider = (*edgeosProvider)(nil)

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &edgeosProvider{version: version}
	}
}

type edgeosProvider struct {
	version string
}

type providerModel struct {
	Host          types.String `tfsdk:"host"`
	Port          types.Int64  `tfsdk:"port"`
	Username      types.String `tfsdk:"username"`
	Password      types.String `tfsdk:"password"`
	Insecure      types.Bool   `tfsdk:"insecure"`
	CACertificate types.String `tfsdk:"ca_certificate"`
	Timeout       types.String `tfsdk:"timeout"`
	Transport     types.String `tfsdk:"transport"`
	Save          types.Bool   `tfsdk:"save"`
}

// providerData is handed to resources and data sources.
type providerData struct {
	client *client.Client
	// username is the login user, which edgeos_system_login_user refuses
	// to delete by default.
	username string
}

func (p *edgeosProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "edgeos"
	resp.Version = p.version
}

func (p *edgeosProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Ubiquiti EdgeRouter configuration (EdgeOS) through the API the EdgeOS web UI uses. " +
			"Every attribute can also be set with an environment variable, shown in each description.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				MarkdownDescription: "Router address: a host name or IP, optionally with `:port`, or an `https://` URL. Env: `EDGEOS_HOST`.",
				Optional:            true,
			},
			"port": schema.Int64Attribute{
				MarkdownDescription: "HTTPS port of the web UI. Defaults to `443`. Env: `EDGEOS_PORT`.",
				Optional:            true,
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Login user. It needs the `admin` level to change config. Env: `EDGEOS_USERNAME`.",
				Optional:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Login password. Env: `EDGEOS_PASSWORD`.",
				Optional:            true,
				Sensitive:           true,
			},
			"insecure": schema.BoolAttribute{
				MarkdownDescription: "Skip TLS certificate verification. EdgeRouters ship a self-signed certificate, so this is " +
					"often needed; prefer `ca_certificate` where you can. Defaults to `false`. Env: `EDGEOS_INSECURE`.",
				Optional: true,
			},
			"ca_certificate": schema.StringAttribute{
				MarkdownDescription: "PEM encoded certificate to trust for the router, for example the router's own self-signed " +
					"certificate. Env: `EDGEOS_CA_CERTIFICATE` (PEM text or a path to a file).",
				Optional: true,
			},
			"timeout": schema.StringAttribute{
				MarkdownDescription: "Timeout for each request as a Go duration, such as `30s` or `2m`. Commits of large firewall " +
					"changes can take a while on small routers. Defaults to `60s`. Env: `EDGEOS_TIMEOUT`.",
				Optional: true,
			},
			"transport": schema.StringAttribute{
				MarkdownDescription: "How to talk to the router. Only `api` (the web UI API) is available at the moment; `ssh` is " +
					"planned. Defaults to `api`. Env: `EDGEOS_TRANSPORT`.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.OneOf("api")},
			},
			"save": schema.BoolAttribute{
				MarkdownDescription: "Persist each commit to `/config/config.boot` so it survives a reboot. Defaults to `true`. " +
					"The `api` transport always saves, so `false` is rejected until a transport that can commit without " +
					"saving exists. Env: `EDGEOS_SAVE`.",
				Optional: true,
			},
		},
	}
}

// envOr returns the attribute value if set, else the environment variable.
func envOr(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *edgeosProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, v := range map[string]interface{ IsUnknown() bool }{
		"host": cfg.Host, "port": cfg.Port, "username": cfg.Username, "password": cfg.Password,
		"insecure": cfg.Insecure, "ca_certificate": cfg.CACertificate, "timeout": cfg.Timeout,
		"transport": cfg.Transport, "save": cfg.Save,
	} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider attribute",
				fmt.Sprintf("The provider cannot connect while %q is unknown. Set it to a known value, or apply the resources it depends on first with -target.", name))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	host := envOr(cfg.Host, "EDGEOS_HOST")
	username := envOr(cfg.Username, "EDGEOS_USERNAME")
	password := envOr(cfg.Password, "EDGEOS_PASSWORD")
	transport := envOr(cfg.Transport, "EDGEOS_TRANSPORT")
	if transport == "" {
		transport = "api"
	}

	port := 0
	if !cfg.Port.IsNull() {
		port = int(cfg.Port.ValueInt64())
	} else if s := os.Getenv("EDGEOS_PORT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("port"), "Invalid EDGEOS_PORT", fmt.Sprintf("%q is not a number.", s))
		}
		port = n
	}
	if port < 0 || port > 65535 {
		resp.Diagnostics.AddAttributeError(path.Root("port"), "Invalid port", fmt.Sprintf("%d is not a valid TCP port.", port))
	}

	insecure, ok := boolSetting(cfg.Insecure, "EDGEOS_INSECURE", false)
	if !ok {
		resp.Diagnostics.AddAttributeError(path.Root("insecure"), "Invalid EDGEOS_INSECURE", "Use true or false.")
	}
	save, ok := boolSetting(cfg.Save, "EDGEOS_SAVE", true)
	if !ok {
		resp.Diagnostics.AddAttributeError(path.Root("save"), "Invalid EDGEOS_SAVE", "Use true or false.")
	}

	timeout := 60 * time.Second
	if s := envOr(cfg.Timeout, "EDGEOS_TIMEOUT"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Invalid timeout",
				fmt.Sprintf("%q is not a positive Go duration such as 30s or 2m.", s))
		}
		timeout = d
	}

	var caPEM []byte
	if s := envOr(cfg.CACertificate, "EDGEOS_CA_CERTIFICATE"); s != "" {
		if strings.Contains(s, "-----BEGIN") {
			caPEM = []byte(s)
		} else {
			b, err := os.ReadFile(s)
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("ca_certificate"), "Cannot read CA certificate", err.Error())
			}
			caPEM = b
		}
	}

	if transport != "api" {
		resp.Diagnostics.AddAttributeError(path.Root("transport"), "Unsupported transport",
			fmt.Sprintf("Transport %q is not available. Use \"api\".", transport))
	}
	if !save {
		resp.Diagnostics.AddAttributeError(path.Root("save"), "save = false is not supported by the api transport",
			"The EdgeOS web API commits and saves every change in one step, so there is no way to commit without saving. "+
				"Remove save = false (or EDGEOS_SAVE=false).")
	}

	for _, m := range []struct{ attr, env, val string }{
		{"host", "EDGEOS_HOST", host},
		{"username", "EDGEOS_USERNAME", username},
		{"password", "EDGEOS_PASSWORD", password},
	} {
		if m.val == "" {
			resp.Diagnostics.AddAttributeError(path.Root(m.attr), "Missing "+m.attr,
				fmt.Sprintf("Set %q in the provider block or the %s environment variable.", m.attr, m.env))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	tr, err := webapi.New(webapi.Config{
		Host:      host,
		Port:      port,
		Username:  username,
		Password:  password,
		Insecure:  insecure,
		CACertPEM: caPEM,
		Timeout:   timeout,
		UserAgent: fmt.Sprintf("terraform-provider-edgeos/%s", p.version),
		Logf: func(format string, args ...any) {
			tflog.Debug(ctx, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid EdgeOS connection settings", err.Error())
		return
	}
	data := &providerData{client: client.New(tr), username: username}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func boolSetting(v types.Bool, env string, def bool) (bool, bool) {
	if !v.IsNull() {
		return v.ValueBool(), true
	}
	s := os.Getenv(env)
	if s == "" {
		return def, true
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return def, false
	}
	return b, true
}

func (p *edgeosProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newConfigResource,
		newFirewallNameResource(),
		newFirewallRuleResource(),
		newNATRuleResource(),
		newPortForwardResource(),
		newPortForwardSettingsResource(),
		newStaticRouteResource(),
		newStaticInterfaceRouteResource(),
		newInterfaceEthernetResource(),
		newInterfaceVifResource(),
		newInterfaceSwitchResource(),
		newSystemResource(),
		newLoginUserResource,
		newLoginUserPublicKeyResource,
		newServiceSSHResource(),
		newServiceGUIResource,
		newDHCPSharedNetworkResource(),
		newDHCPStaticMappingResource(),
		newWireguardInterfaceResource(),
		newWireguardPeerResource(),
		newFirewallSettingsResource(),
		newUPnPResource(),
		newDHCPServerSettingsResource(),
	}
}

func (p *edgeosProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newConfigDataSource,
		newSystemInfoDataSource,
	}
}
