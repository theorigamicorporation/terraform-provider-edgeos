// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/provider"
)

//go:generate terraform fmt -recursive ./examples/
//go:generate go tool tfplugindocs generate --provider-name edgeos

// version is set by goreleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		// The same binary is published to the Terraform and OpenTofu
		// registries; OpenTofu maps this address to its own registry.
		Address: "registry.terraform.io/theorigamicorporation/edgeos",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
