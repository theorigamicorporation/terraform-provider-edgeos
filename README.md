# EdgeOS provider for Terraform and OpenTofu

[![CI](https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/ci.yml/badge.svg)](https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/theorigamicorporation/terraform-provider-edgeos?sort=semver)](https://github.com/theorigamicorporation/terraform-provider-edgeos/releases)
[![License: MPL-2.0](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/theorigamicorporation/terraform-provider-edgeos)](https://goreportcard.com/report/github.com/theorigamicorporation/terraform-provider-edgeos)

Manage Ubiquiti EdgeRouter configuration (EdgeOS) as code: firewall rule sets,
NAT, port forwards, interfaces and VLANs, DHCP, static routes, WireGuard and
system settings, plus a generic resource for any other config node.

One provider binary serves both tools. The source address
`theorigamicorporation/edgeos` works in Terraform and in OpenTofu, and both
registries publish the same signed release assets.

## Install

### Terraform

```hcl
terraform {
  required_providers {
    edgeos = {
      source  = "theorigamicorporation/edgeos"
      version = "~> 0.1"
    }
  }
}
```

### OpenTofu

```hcl
terraform {
  required_providers {
    edgeos = {
      source  = "theorigamicorporation/edgeos"
      version = "~> 0.1"
    }
  }
}
```

It is the same block. OpenTofu resolves the short address against
`registry.opentofu.org` and Terraform against `registry.terraform.io`. Run
`terraform init` or `tofu init` as usual.

## Quick start

```hcl
provider "edgeos" {
  host     = "192.168.1.1"
  username = "admin"
  # password from EDGEOS_PASSWORD
  insecure = true # EdgeRouters ship a self-signed certificate
}

resource "edgeos_firewall_name" "wan_local" {
  name           = "WAN_LOCAL"
  description    = "WAN to router"
  default_action = "drop"
}

resource "edgeos_firewall_rule" "wan_local_established" {
  ruleset = edgeos_firewall_name.wan_local.name
  number  = 10
  action  = "accept"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "wan_local_wireguard" {
  ruleset  = edgeos_firewall_name.wan_local.name
  number   = 30
  action   = "accept"
  protocol = "udp"
  destination = {
    port = "51820"
  }
}
```

A complete example with a VLAN WAN, firewall, NAT, port forwards, DHCP and
WireGuard is in [`examples/full`](examples/full). Every resource and data
source has its own example under [`examples/`](examples) and a page under
[`docs/`](docs).

## Authentication

| Attribute        | Environment variable    | Default |
|------------------|-------------------------|---------|
| `host`           | `EDGEOS_HOST`           | none    |
| `port`           | `EDGEOS_PORT`           | `443`   |
| `username`       | `EDGEOS_USERNAME`       | none    |
| `password`       | `EDGEOS_PASSWORD`       | none    |
| `insecure`       | `EDGEOS_INSECURE`       | `false` |
| `ca_certificate` | `EDGEOS_CA_CERTIFICATE` | none    |
| `timeout`        | `EDGEOS_TIMEOUT`        | `60s`   |
| `transport`      | `EDGEOS_TRANSPORT`      | `api`   |
| `save`           | `EDGEOS_SAVE`           | `true`  |

The user needs the `admin` level; operators cannot change config and the API
hides secrets such as WireGuard keys from them.

Rather than turning off certificate checks with `insecure = true`, you can
trust the router's own certificate. Fetch it once and pass it with
`ca_certificate` or `EDGEOS_CA_CERTIFICATE` (PEM text or a file path):

```sh
openssl s_client -connect 192.168.1.1:443 </dev/null 2>/dev/null \
  | openssl x509 > router.pem
```

The certificate has to be valid for the host name or IP you connect to. The
factory certificate of many EdgeRouters is not, in which case `insecure = true`
is the only option until you install a proper one.

## How it talks to the router

EdgeOS has no documented API. The provider uses the JSON API that the EdgeOS
web UI itself uses:

- it logs in with a form POST to `/` and keeps the session and CSRF cookies,
  logging in again when the session expires;
- it reads with `/api/edge/partial.json`, which returns just the subtree it
  needs;
- it writes with `/api/edge/batch.json`, which applies all deletes and sets of
  one resource change, commits them as one transaction and saves to
  `config.boot`. If EdgeOS refuses the commit nothing is changed, and the
  error message from EdgeOS is shown with the path it refers to.

Writes from one provider instance are serialised, because EdgeOS fails when two
commits overlap. Terraform still plans and reads in parallel.

An SSH transport (Vyatta CLI through an interactive session) is planned; the
`transport` attribute already exists for it. Until then `save = false` is
rejected, because the web API always saves.

## Ownership rules

Each resource owns a fixed set of attributes below one config node and leaves
everything else alone. For example `edgeos_firewall_name` never touches the
rules inside the rule set, and `edgeos_interface_ethernet` never touches VLAN
interfaces on the port. Destroying a resource that represents a named node
(a rule set, a rule, a VLAN) deletes that node; destroying a resource for
something that always exists (a physical port, the system settings) only
removes the attributes the resource manages.

Changes made in the web UI or the CLI to managed attributes show up as drift
on the next plan and are reverted on apply.

For anything without a dedicated resource use `edgeos_config`, which manages a
single value, a list of values or a whole subtree as JSON:

```hcl
resource "edgeos_config" "dns_cache" {
  path  = ["service", "dns", "forwarding", "cache-size"]
  value = "1000"
}
```

## Supported routers

Tested against EdgeOS 3.0.x on an EdgeRouter X (ER-X, platform `e50`). The web
API has been stable since EdgeOS 1.x, so 2.0.x and other EdgeRouter models
(ER-4, ER-6P, ER-12, ER Lite, ER Infinity) are expected to work; reports are
welcome. UniFi gateways (USG, UDM) run different software and are not
supported.

## Limitations

- IPv6 firewall (`firewall ipv6-name`), zone policies, QoS, VPNs other than
  WireGuard and many services have no dedicated resource yet. Use
  `edgeos_config` for them.
- EdgeOS rejects values containing a double quote; the provider checks this at
  plan time.
- Login passwords cannot be read back (EdgeOS stores a hash and blanks the
  plaintext), so they cannot be managed through `edgeos_config` trees.
- The batch API saves every commit; there is no commit-only mode yet.

## Development

Toolchain versions are pinned in [`.tool-versions`](.tool-versions); run
`asdf install` to get the same Go, golangci-lint, Terraform, OpenTofu, just and
goreleaser that CI uses. [`just`](https://just.systems) is the entry point:

```sh
just              # list recipes
just test         # unit tests with the race detector
just testacc      # acceptance tests against the fake router, Terraform and OpenTofu
just cover        # coverage report with the 80% gate
just docs         # regenerate docs/
just fake-server  # run the fake EdgeOS API locally
just install      # install the provider and print dev_overrides snippets
```

The acceptance tests run against an in-memory fake EdgeOS API
(`internal/fakeedgeos`), so no router is needed. Tests against a real router
exist too, but they write to it; see [CONTRIBUTING.md](CONTRIBUTING.md).

To try the examples without a router, start `just fake-server` in one terminal,
export the variables it prints, run `just install`, set up `dev_overrides` as
shown, and run `terraform plan` or `tofu plan` in an example directory.

## License

[MPL-2.0](LICENSE)
