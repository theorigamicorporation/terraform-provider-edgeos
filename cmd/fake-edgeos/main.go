// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Command fake-edgeos runs the in-memory fake EdgeOS API so the examples can
// be tried with terraform or tofu without a router.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8443", "address to listen on")
	user := flag.String("username", fakeedgeos.DefaultUsername, "login user name")
	pass := flag.String("password", fakeedgeos.DefaultPassword, "login password")
	seed := flag.String("seed", "", "optional JSON file with the initial config tree")
	dump := flag.Bool("dump-on-exit", true, "print the config tree as JSON on exit")
	flag.Parse()

	opts := fakeedgeos.Options{Username: *user, Password: *pass}
	if *seed != "" {
		raw, err := os.ReadFile(*seed)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(raw, &opts.Tree); err != nil {
			log.Fatalf("parsing %s: %v", *seed, err)
		}
	}
	srv := fakeedgeos.New(opts)
	url, err := srv.StartOn(*addr)
	if err != nil {
		log.Fatal(err)
	}
	host, port := srv.HostPort()
	fmt.Printf(`Fake EdgeOS API listening on %s (self-signed certificate)

  export EDGEOS_HOST=%s
  export EDGEOS_PORT=%s
  export EDGEOS_USERNAME=%s
  export EDGEOS_PASSWORD='%s'
  export EDGEOS_INSECURE=true

Press Ctrl+C to stop.
`, url, host, port, *user, *pass)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	srv.Close()
	if *dump {
		out, _ := json.MarshalIndent(srv.Tree(), "", "  ")
		fmt.Println(string(out))
	}
}
