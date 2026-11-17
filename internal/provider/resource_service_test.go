// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func TestServiceSSHMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := specOf[serviceSSHModel](newServiceSSHResource())
	cases := []struct {
		name   string
		model  serviceSSHModel
		leaves map[string]configtree.Value
	}{
		{"defaults", serviceSSHModel{
			Port: types.Int64Value(22), ListenAddresses: types.SetNull(types.StringType), ProtocolVersion: types.StringValue("v2"),
			DisablePasswordAuthentication: types.BoolValue(false), DisableHostValidation: types.BoolValue(false), AllowRoot: types.BoolValue(false),
		}, map[string]configtree.Value{
			"port":             configtree.Single("22"),
			"protocol-version": configtree.Single("v2"),
		}},
		{"everything", serviceSSHModel{
			Port: types.Int64Value(2222), ListenAddresses: stringSet([]string{"192.168.1.1", "2001:db8::1"}), ProtocolVersion: types.StringValue("all"),
			DisablePasswordAuthentication: types.BoolValue(true), DisableHostValidation: types.BoolValue(true), AllowRoot: types.BoolValue(true),
		}, map[string]configtree.Value{
			"port":                            configtree.Single("2222"),
			"listen-address":                  configtree.UnorderedMulti("192.168.1.1", "2001:db8::1"),
			"protocol-version":                configtree.Single("all"),
			"disable-password-authentication": configtree.Flag(),
			"disable-host-validation":         configtree.Flag(),
			"allow-root":                      configtree.Flag(),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			l := spec.ToLeaves(ctx, &tc.model, &diags)
			if diags.HasError() {
				t.Fatal(diags)
			}
			assertLeaves(t, l, tc.leaves)
			var back serviceSSHModel
			spec.FromTree(ctx, configtree.Build(l), &back, &diags)
			assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), tc.leaves)
			if !back.AllowRoot.Equal(tc.model.AllowRoot) || !back.ListenAddresses.Equal(tc.model.ListenAddresses) {
				t.Errorf("read back %+v", back)
			}
		})
	}
	if got := spec.Owned(nil); len(got) != 4 {
		t.Errorf("owned = %v", got)
	}
	assertLeaves(t, spec.Reset(nil), map[string]configtree.Value{"port": configtree.Single("22"), "protocol-version": configtree.Single("v2")})
}

func TestServiceGUIMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	spec := serviceGUISpec
	m := serviceGUIModel{
		HTTPPort: types.Int64Value(8080), HTTPSPort: types.Int64Value(8443), ListenAddresses: stringSet([]string{"192.168.1.1"}),
		OlderCiphers: types.BoolValue(false), CAFile: types.StringValue("/config/auth/ca.pem"), CertFile: types.StringValue("/config/auth/gui.pem"),
	}
	want := map[string]configtree.Value{
		"http-port":      configtree.Single("8080"),
		"https-port":     configtree.Single("8443"),
		"listen-address": configtree.UnorderedMulti("192.168.1.1"),
		"older-ciphers":  configtree.Single("disable"),
		"ca-file":        configtree.Single("/config/auth/ca.pem"),
		"cert-file":      configtree.Single("/config/auth/gui.pem"),
	}
	var diags diag.Diagnostics
	l := spec.ToLeaves(ctx, &m, &diags)
	assertLeaves(t, l, want)
	var back serviceGUIModel
	spec.FromTree(ctx, configtree.Build(l), &back, &diags)
	assertLeaves(t, spec.ToLeaves(ctx, &back, &diags), want)
	if diags.HasError() {
		t.Fatal(diags)
	}
	assertLeaves(t, spec.Reset(nil), map[string]configtree.Value{
		"http-port": configtree.Single("80"), "https-port": configtree.Single("443"), "older-ciphers": configtree.Single("enable"),
	})

	same := m
	same.HTTPSPort = types.Int64Value(8080)
	validateServiceGUI(ctx, &same, &diags)
	if !diags.HasError() {
		t.Error("equal ports must fail")
	}
	var d2 diag.Diagnostics
	defaults := serviceGUIModel{HTTPPort: types.Int64Null(), HTTPSPort: types.Int64Value(80)}
	validateServiceGUI(ctx, &defaults, &d2)
	if !d2.HasError() {
		t.Error("https_port = 80 clashes with the default http_port")
	}

	if guiEndpointChanges(&m, &m) {
		t.Error("no change reported as a change")
	}
	moved := m
	moved.HTTPSPort = types.Int64Value(9443)
	if !guiEndpointChanges(&m, &moved) {
		t.Error("https_port change not noticed")
	}
	moved = m
	moved.HTTPPort = types.Int64Value(81)
	if guiEndpointChanges(&m, &moved) {
		t.Error("http_port does not move the provider's endpoint")
	}
}

func TestSingletonImport(t *testing.T) {
	t.Parallel()
	if attrs, err := singletonImport("ssh", "ssh"); err != nil || attrs["id"].(types.String).ValueString() != "ssh" {
		t.Errorf("ssh: %v %v", attrs, err)
	}
	if _, err := singletonImport("gui", "ssh"); err == nil {
		t.Error("wrong ID accepted")
	}
}

// Acceptance tests against the fake router.

const sshConfig = `
resource "edgeos_service_ssh" "this" {
  port                            = 2222
  listen_addresses                = ["192.168.1.1", "2001:db8::1"]
  disable_password_authentication = true
  disable_host_validation         = true
}`

func TestAccServiceSSH(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			// Destroy keeps SSH on with the defaults and the unmodelled option.
			e.checkValue("service ssh", map[string]any{"port": "22", "protocol-version": "v2", "ciphers": "aes128-ctr"}),
		),
		Steps: []resource.TestStep{
			{
				// Adopt: allow-root set by hand goes away, an unmodelled
				// option stays.
				PreConfig: func() {
					e.set("service ssh allow-root", configtree.Flag())()
					e.set("service ssh ciphers", configtree.Single("aes128-ctr"))()
				},
				Config: e.config(sshConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_service_ssh.this", "id", "ssh"),
					resource.TestCheckResourceAttr("edgeos_service_ssh.this", "protocol_version", "v2"),
					resource.TestCheckResourceAttr("edgeos_service_ssh.this", "allow_root", "false"),
					e.checkValue("service ssh", map[string]any{
						"port":                            "2222",
						"protocol-version":                "v2",
						"listen-address":                  []string{"192.168.1.1", "2001:db8::1"},
						"disable-password-authentication": nil,
						"disable-host-validation":         nil,
						"ciphers":                         "aes128-ctr",
					}),
				),
			},
			{
				// Leaving attributes out puts their defaults back.
				Config: e.config(`
resource "edgeos_service_ssh" "this" {
  disable_password_authentication = true
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_ssh.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service ssh port", "22"),
					e.checkAbsent("service ssh listen-address"),
					e.checkAbsent("service ssh disable-host-validation"),
					e.checkValue("service ssh disable-password-authentication", nil),
				),
			},
			{
				Config: e.config(sshConfig),
				Check:  e.checkValue("service ssh port", "2222"),
			},
			{
				ResourceName:      "edgeos_service_ssh.this",
				ImportState:       true,
				ImportStateId:     "ssh",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_service_ssh.this",
				ImportState:     true,
				ImportStateId:   "ssh",
				ImportStateKind: resource.ImportBlockWithID,
			},
			{
				// Drift: port and a flag changed by hand.
				PreConfig: func() {
					e.set("service ssh port", configtree.Single("22"))()
					e.del("service ssh disable-password-authentication")()
					e.set("service ssh allow-root", configtree.Flag())()
				},
				Config: e.config(sshConfig),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_ssh.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service ssh port", "2222"),
					e.checkValue("service ssh disable-password-authentication", nil),
					e.checkAbsent("service ssh allow-root"),
				),
			},
			{
				// Drift: SSH switched off by hand. The next apply turns it on.
				PreConfig: e.del("service ssh"),
				Config:    e.config(sshConfig),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_ssh.this", plancheck.ResourceActionCreate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service ssh port", "2222"),
					e.checkValue("service ssh protocol-version", "v2"),
				),
			},
			{
				// The unmodelled option is gone with the node; put it back
				// for the destroy check.
				PreConfig: e.set("service ssh ciphers", configtree.Single("aes128-ctr")),
				Config:    e.config(sshConfig),
				PlanOnly:  true,
			},
		},
	})
}

func TestAccServiceSSHErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The fake's web UI is on 443, so SSH cannot take that port.
				Config: e.config(`
resource "edgeos_service_ssh" "this" {
  port = 443
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*already in use by the web UI`),
			},
		},
	})
	if got, _ := e.srv.Get(cliPath("service ssh port")); got != "22" {
		t.Fatalf("port changed after a failed commit: %v", got)
	}
}

func TestAccServiceSSHValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		`port = 0`:                            `between 1 and 65535`,
		`port = 70000`:                        `between 1 and 65535`,
		`protocol_version = "v3"`:             `value must be one of`,
		`listen_addresses = []`:               `at least 1`,
		`listen_addresses = ["192.0.2.0/24"]`: `Invalid IP address`,
		`allow_root = "maybe"`:                `bool`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_service_ssh\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)` + strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}

const guiConfig = `
resource "edgeos_service_gui" "this" {
  http_port        = 8080
  listen_addresses = ["192.168.1.1"]
  older_ciphers    = false
  cert_file        = "/config/auth/gui.pem"
}`

func TestAccServiceGUI(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: e.checkValue("service gui", map[string]any{
			"http-port": "80", "https-port": "443", "older-ciphers": "enable",
		}),
		Steps: []resource.TestStep{
			{
				Config: e.config(guiConfig),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("edgeos_service_gui.this", "id", "gui"),
					resource.TestCheckResourceAttr("edgeos_service_gui.this", "https_port", "443"),
					e.checkValue("service gui", map[string]any{
						"http-port":      "8080",
						"https-port":     "443",
						"listen-address": []string{"192.168.1.1"},
						"older-ciphers":  "disable",
						"cert-file":      "/config/auth/gui.pem",
					}),
				),
			},
			{
				// The fake keeps serving on its port, so the provider can
				// still read back after https_port moves.
				Config: e.config(`
resource "edgeos_service_gui" "this" {
  https_port = 8443
  ca_file    = "/config/auth/ca.pem"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_gui.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service gui", map[string]any{
						"http-port": "80", "https-port": "8443", "older-ciphers": "enable", "ca-file": "/config/auth/ca.pem",
					}),
				),
			},
			{
				Config: e.config(guiConfig),
				Check:  e.checkValue("service gui https-port", "443"),
			},
			{
				ResourceName:      "edgeos_service_gui.this",
				ImportState:       true,
				ImportStateId:     "gui",
				ImportStateVerify: true,
			},
			{
				ResourceName:    "edgeos_service_gui.this",
				ImportState:     true,
				ImportStateId:   "gui",
				ImportStateKind: resource.ImportBlockWithID,
			},
			{
				// Drift: ciphers and certificate changed by hand.
				PreConfig: func() {
					e.set("service gui older-ciphers", configtree.Single("enable"))()
					e.del("service gui cert-file")()
					e.set("service gui ca-file", configtree.Single("/config/auth/other.pem"))()
				},
				Config: e.config(guiConfig),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("edgeos_service_gui.this", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					e.checkValue("service gui older-ciphers", "disable"),
					e.checkValue("service gui cert-file", "/config/auth/gui.pem"),
					e.checkAbsent("service gui ca-file"),
				),
			},
		},
	})
}

func TestAccServiceGUIErrors(t *testing.T) {
	e := newAccEnv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { e.srv.FailNextCommit("lighttpd: cannot read /config/auth/gui.pem") },
				Config:      e.config(guiConfig),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*cannot read`),
			},
			{
				// The fake's SSH server is on 22.
				Config: e.config(`
resource "edgeos_service_gui" "this" {
  http_port = 22
}`),
				ExpectError: regexp.MustCompile(`(?s)EdgeOS rejected the change.*Port 22 is already in use`),
			},
		},
	})
	if got, _ := e.srv.Get(cliPath("service gui http-port")); got != "80" {
		t.Fatalf("http-port changed after a failed commit: %v", got)
	}
}

func TestAccServiceGUIValidation(t *testing.T) {
	e := newAccEnv(t)
	for body, want := range map[string]string{
		`http_port = 443`:                    `Same port twice`,
		`https_port = 0`:                     `between 1 and 65535`,
		`listen_addresses = ["gw.example"]`:  `Invalid IP address`,
		`cert_file = "gui.pem"`:              `absolute path`,
		`ca_file = "/config/auth/my ca.pem"`: `absolute path`,
	} {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      e.config("resource \"edgeos_service_gui\" \"x\" {\n  " + body + "\n}"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)` + strings.ReplaceAll(want, " ", `\s+`)),
			}},
		})
	}
	if e.srv.Stats().Batches != 0 {
		t.Fatal("validation errors must not reach the router")
	}
}
