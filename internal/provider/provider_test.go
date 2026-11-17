// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/pem"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
)

// testAccProtoV6ProviderFactories runs the provider in-process for
// acceptance tests. It works with both terraform and tofu; pick the binary
// with TF_ACC_TERRAFORM_PATH.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"edgeos": providerserver.NewProtocol6WithError(New("test")()),
}

// accEnv is one fake router per test.
type accEnv struct {
	t   *testing.T
	srv *fakeedgeos.Server
}

func newAccEnv(t *testing.T, opts ...fakeedgeos.Options) *accEnv {
	t.Helper()
	var o fakeedgeos.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	srv := fakeedgeos.New(o)
	srv.Start()
	t.Cleanup(srv.Close)
	return &accEnv{t: t, srv: srv}
}

// providerBlock returns a provider block pointing at the fake router.
func (e *accEnv) providerBlock(extra ...string) string {
	host, port := e.srv.HostPort()
	return fmt.Sprintf(`
provider "edgeos" {
  host     = %q
  port     = %s
  username = %q
  password = %q
  insecure = true
  timeout  = "10s"
  %s
}
`, host, port, fakeedgeos.DefaultUsername, fakeedgeos.DefaultPassword, strings.Join(extra, "\n  "))
}

// config prepends the provider block to body.
func (e *accEnv) config(body string) string {
	return e.providerBlock() + body
}

func cliPath(s string) configtree.Path {
	p, err := configtree.ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

// checkValue asserts that the fake router holds want at path. want is a
// string, a []string, nil for a valueless node, or a map for a subtree.
func (e *accEnv) checkValue(path string, want any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, ok := e.srv.Get(cliPath(path))
		if !ok {
			return fmt.Errorf("%q is missing on the router", path)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("%q = %#v, want %#v", path, got, want)
		}
		return nil
	}
}

// checkAbsent asserts that path does not exist on the fake router.
func (e *accEnv) checkAbsent(path string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got, ok := e.srv.Get(cliPath(path)); ok {
			return fmt.Errorf("%q still exists: %#v", path, got)
		}
		return nil
	}
}

// destroyed is a CheckDestroy that asserts every path is gone.
func (e *accEnv) destroyed(paths ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for _, p := range paths {
			if err := e.checkAbsent(p)(s); err != nil {
				return err
			}
		}
		return nil
	}
}

// set and del change the router behind Terraform's back.
func (e *accEnv) set(path string, v configtree.Value) func() {
	return func() { e.srv.Set(cliPath(path), v) }
}

func (e *accEnv) del(path string) func() {
	return func() { e.srv.Delete(cliPath(path)) }
}

var (
	reRejected = regexp.MustCompile(`EdgeOS rejected the change`)
	reLogin    = regexp.MustCompile(`EdgeOS login failed`)
)

// certPEM returns the fake router's self-signed certificate as PEM.
func certPEM(s *fakeedgeos.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
}
