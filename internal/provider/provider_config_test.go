// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
)

// TestAccProviderEnv configures the provider only through environment
// variables, the way CI pipelines usually do it.
func TestAccProviderEnv(t *testing.T) {
	e := newAccEnv(t)
	host, port := e.srv.HostPort()
	t.Setenv("EDGEOS_HOST", host)
	t.Setenv("EDGEOS_PORT", port)
	t.Setenv("EDGEOS_USERNAME", fakeedgeos.DefaultUsername)
	t.Setenv("EDGEOS_PASSWORD", fakeedgeos.DefaultPassword)
	t.Setenv("EDGEOS_INSECURE", "true")
	t.Setenv("EDGEOS_TIMEOUT", "5s")
	t.Setenv("EDGEOS_TRANSPORT", "api")
	t.Setenv("EDGEOS_SAVE", "true")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "edgeos_system_info" "this" {}`,
			Check:  resource.TestCheckResourceAttr("data.edgeos_system_info.this", "platform", "e50"),
		}},
	})
}

// TestAccProviderCACertificate trusts the fake router's certificate instead
// of skipping verification, from a file and inline.
func TestAccProviderCACertificate(t *testing.T) {
	e := newAccEnv(t)
	_, port := e.srv.HostPort()
	pemFile := filepath.Join(t.TempDir(), "router.pem")
	if err := os.WriteFile(pemFile, certPEM(e.srv), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDGEOS_CA_CERTIFICATE", pemFile)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "edgeos" {
  host     = "127.0.0.1"
  port     = ` + port + `
  username = "` + fakeedgeos.DefaultUsername + `"
  password = "` + fakeedgeos.DefaultPassword + `"
}
data "edgeos_system_info" "this" {}`,
				Check: resource.TestCheckResourceAttr("data.edgeos_system_info.this", "version", "v3.0.1"),
			},
			{
				Config: `
provider "edgeos" {
  host           = "127.0.0.1"
  port           = ` + port + `
  username       = "` + fakeedgeos.DefaultUsername + `"
  password       = "` + fakeedgeos.DefaultPassword + `"
  ca_certificate = <<-EOT
` + string(certPEM(e.srv)) + `
EOT
}
data "edgeos_system_info" "this" {}`,
				Check: resource.TestCheckResourceAttr("data.edgeos_system_info.this", "version", "v3.0.1"),
			},
		},
	})
}

func TestAccProviderConfigErrors(t *testing.T) {
	e := newAccEnv(t)
	for _, env := range []string{"EDGEOS_HOST", "EDGEOS_USERNAME", "EDGEOS_PASSWORD", "EDGEOS_PORT", "EDGEOS_INSECURE", "EDGEOS_SAVE", "EDGEOS_TIMEOUT", "EDGEOS_TRANSPORT", "EDGEOS_CA_CERTIFICATE"} {
		t.Setenv(env, "")
	}
	host, port := e.srv.HostPort()
	cases := []struct {
		provider string
		want     string
	}{
		{`provider "edgeos" {}`, `Missing host`},
		{`provider "edgeos" {
  host = "` + host + `"
  username = "a"
  password = "b"
  save = false
}`, `save = false is not supported`},
		{`provider "edgeos" {
  host = "` + host + `"
  username = "a"
  password = "b"
  timeout = "soon"
}`, `Invalid timeout`},
		{`provider "edgeos" {
  host = "` + host + `"
  username = "a"
  password = "b"
  transport = "ssh"
}`, `value must be one of`},
		{`provider "edgeos" {
  host = "` + host + `"
  port = 70000
  username = "a"
  password = "b"
}`, `Invalid port`},
		{`provider "edgeos" {
  host = "` + host + `"
  username = "a"
  password = "b"
  ca_certificate = "/does/not/exist.pem"
}`, `Cannot read CA certificate`},
		{`provider "edgeos" {
  host = "ftp://` + host + `"
  username = "a"
  password = "b"
}`, `scheme must be https`},
		{`provider "edgeos" {
  host = "` + host + `"
  port = ` + port + `
  username = "a"
  password = "b"
}`, `certificate`},
	}
	for _, tc := range cases {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{{
				Config:      tc.provider + "\ndata \"edgeos_system_info\" \"this\" {}",
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
	}

	t.Setenv("EDGEOS_PORT", "eighty")
	t.Setenv("EDGEOS_INSECURE", "maybe")
	t.Setenv("EDGEOS_SAVE", "sometimes")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      `data "edgeos_system_info" "this" {}`,
			ExpectError: regexp.MustCompile(`(?s)Invalid EDGEOS_PORT.*Invalid EDGEOS_INSECURE.*Invalid EDGEOS_SAVE|Invalid EDGEOS_`),
		}},
	})
}
