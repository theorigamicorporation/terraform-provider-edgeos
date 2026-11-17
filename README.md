# EdgeOS provider for Terraform and OpenTofu

[![CI](https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/theorigamicorporation/terraform-provider-edgeos?sort=semver)](https://github.com/theorigamicorporation/terraform-provider-edgeos/releases/latest)
[![Terraform Registry](https://img.shields.io/badge/terraform-registry-7B42BC?logo=terraform)](https://registry.terraform.io/providers/theorigamicorporation/edgeos/latest)
[![OpenTofu Registry](https://img.shields.io/badge/opentofu-registry-FFDA18?logo=opentofu)](https://search.opentofu.org/provider/theorigamicorporation/edgeos/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/theorigamicorporation/terraform-provider-edgeos)](go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/theorigamicorporation/terraform-provider-edgeos)](https://goreportcard.com/report/github.com/theorigamicorporation/terraform-provider-edgeos)
[![License: MPL-2.0](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)

Manage a Ubiquiti EdgeRouter as code. Firewall rule sets, NAT, port forwards,
interfaces and VLANs, DHCP, static routes, WireGuard and system settings each
have a resource, and `edgeos_config` covers any other node of the EdgeOS
config tree. Changes go through the same API the EdgeOS web UI uses and are
committed atomically, so a rejected change leaves the router as it was.

One binary serves Terraform and OpenTofu: the source address
`theorigamicorporation/edgeos` works in both, and both registries publish the
same signed release assets.

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

An EdgeRouter X with the ISP on VLAN 100 of `eth0` and the LAN on `eth1`:

```hcl
provider "edgeos" {
  host     = "192.168.1.1"
  username = "admin"
  # password from EDGEOS_PASSWORD
  insecure = true # EdgeRouters ship a self-signed certificate
}

resource "edgeos_firewall_name" "wan_local" {
  name           = "WAN_LOCAL"
  description    = "Internet to the router"
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
  number   = 20
  action   = "accept"
  protocol = "udp"
  destination = {
    port = "51820"
  }
}

resource "edgeos_interface_vif" "wan" {
  interface      = "eth0"
  vlan           = 100
  description    = "ISP WAN"
  addresses      = ["dhcp"]
  firewall_local = edgeos_firewall_name.wan_local.name
}

resource "edgeos_nat_rule" "masquerade" {
  number             = 5000
  type               = "masquerade"
  description        = "LAN to the internet"
  outbound_interface = "eth0.100"
  depends_on         = [edgeos_interface_vif.wan]
}

resource "edgeos_dhcp_shared_network" "lan" {
  name          = "LAN"
  authoritative = true
  subnets = {
    "192.168.1.0/24" = {
      default_router = "192.168.1.1"
      dns_servers    = ["192.168.1.1"]
      ranges         = [{ start = "192.168.1.100", stop = "192.168.1.199" }]
    }
  }
}

resource "edgeos_dhcp_static_mapping" "nas" {
  shared_network = edgeos_dhcp_shared_network.lan.name
  subnet         = "192.168.1.0/24"
  name           = "nas"
  ip_address     = "192.168.1.20"
  mac_address    = "02:00:5e:00:53:20"
}
```

`terraform plan` (or `tofu plan`) then shows exactly which EdgeOS nodes change.
A complete office setup with port forwards, a guest VLAN and WireGuard is in
[`examples/full`](examples/full); every resource and data source has its own
example under [`examples/`](examples) and a page under [`docs/`](docs).

## Resources and data sources

| Resource | Manages |
|----------|---------|
| [`edgeos_config`](docs/resources/config.md) | any config node: a value, a list or a whole subtree |
| [`edgeos_firewall_name`](docs/resources/firewall_name.md) | an IPv4 firewall rule set |
| [`edgeos_firewall_rule`](docs/resources/firewall_rule.md) | one rule in a rule set |
| [`edgeos_nat_rule`](docs/resources/nat_rule.md) | source, destination and masquerade NAT rules |
| [`edgeos_port_forward`](docs/resources/port_forward.md) | one port forward rule |
| [`edgeos_port_forward_settings`](docs/resources/port_forward_settings.md) | WAN/LAN interfaces, hairpin NAT, auto firewall |
| [`edgeos_interface_ethernet`](docs/resources/interface_ethernet.md) | settings of a physical port |
| [`edgeos_interface_vif`](docs/resources/interface_vif.md) | a VLAN interface |
| [`edgeos_dhcp_shared_network`](docs/resources/dhcp_shared_network.md) | a DHCP server network with its subnets |
| [`edgeos_dhcp_static_mapping`](docs/resources/dhcp_static_mapping.md) | a DHCP reservation |
| [`edgeos_static_route`](docs/resources/static_route.md) | a static route via next hops or a blackhole |
| [`edgeos_static_interface_route`](docs/resources/static_interface_route.md) | a static route via an interface |
| [`edgeos_wireguard_interface`](docs/resources/wireguard_interface.md) | a WireGuard interface |
| [`edgeos_wireguard_peer`](docs/resources/wireguard_peer.md) | a WireGuard peer |
| [`edgeos_system`](docs/resources/system.md) | host name, domain, time zone, name servers, NTP |
| [`edgeos_system_login_user`](docs/resources/system_login_user.md) | a login user with a write-only password |

| Data source | Returns |
|-------------|---------|
| [`edgeos_config`](docs/data-sources/config.md) | any config node, or whether it exists |
| [`edgeos_system_info`](docs/data-sources/system_info.md) | firmware version, platform, host name |

## Authentication and connection

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

## Transport

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

`api` is the only transport today. An SSH transport driving the Vyatta CLI is
planned, and the `transport` attribute already exists for it. Until then
`save = false` is rejected, because the web API always saves.

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

| | |
|---|---|
| Tested | EdgeOS 3.0.x on an EdgeRouter X (ER-X, platform `e50`) |
| Expected to work | EdgeOS 2.0.x and 1.10.x, and the other EdgeRouters: ER-X SFP, ER-10X, ER-4, ER-6P, ER-12, ER Lite, ER PoE, ER-8, ER Pro, ER Infinity |
| Not supported | UniFi gateways (USG, UDM, UXG), which run different software |

The web API has not changed in a breaking way since EdgeOS 1.x. If you run the
provider on another model or release, a short note in an issue helps.

Terraform 1.13 and later and OpenTofu 1.11 and later are tested in CI.

## Limitations

- No dedicated resources yet for the IPv6 firewall (`firewall ipv6-name`),
  firewall groups, zone policies, QoS, VPNs other than WireGuard, DNS
  forwarding and dynamic DNS. Manage them with `edgeos_config`.
- The web API saves every commit to `config.boot`; there is no commit-only
  mode until the SSH transport exists.
- Destroying a named node (a rule set, a VLAN, a DHCP network) deletes it with
  everything below it, including children Terraform does not manage.
- The router serialises commits, so large plans apply one resource at a time.

## EdgeOS quirks worth knowing

- **Double quotes are not allowed in any value.** EdgeOS rejects them with
  "Cannot use the double quote character in a value string". The provider
  catches this at plan time.
- **Passwords need a symbol.** EdgeOS 3.x refuses a `plaintext-password` made
  only of letters and digits.
- **Passwords cannot be read back.** After a commit EdgeOS stores a hash in
  `encrypted-password` and keeps `plaintext-password ""` next to it.
  `edgeos_system_login_user` treats the password as write-only; do not manage
  passwords through an `edgeos_config` tree.
- **Multi-valued nodes are lists.** Nodes such as `address`, `name-server` or
  `dns-server` come back as lists even with one value. In an `edgeos_config`
  tree write them as lists, or every plan shows a change.
- **Script commits break firewall changes on 3.0.1.** Commits run through
  `/opt/vyatta/etc/functions/script-template` fail for firewall rule sets with
  `iptables-restore: line 2 failed`, while the same commands in an interactive
  session succeed. The web API is not affected; the planned SSH transport will
  use an interactive session for this reason.
- **Non-admin users see less.** The API hides secrets such as WireGuard keys
  from operator level users, and they cannot commit. Use an admin user.

## Development

Toolchain versions are pinned in [`.tool-versions`](.tool-versions) and CI
reads the same file. Run `asdf install` to get the same Go, golangci-lint,
Terraform, OpenTofu, just and goreleaser. [`just`](https://just.systems) is the
entry point:

```sh
just              # list recipes
just test         # unit tests with the race detector
just testacc      # acceptance tests against the fake router, Terraform and OpenTofu
just lint         # golangci-lint
just cover        # coverage report with the 80% gate (CI enforces it)
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

## Contributing and security

Bug reports, model and firmware reports and pull requests are welcome; see
[CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as
described in [SECURITY.md](SECURITY.md). This project follows the
[Contributor Covenant](CODE_OF_CONDUCT.md). Changes are listed in
[CHANGELOG.md](CHANGELOG.md).

## License

[Mozilla Public License 2.0](LICENSE)
