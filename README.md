<div align="center">

<img src="assets/logo.svg" alt="" width="112" height="112">

# EdgeOS provider for Terraform and OpenTofu

**Manage Ubiquiti EdgeRouter configuration as code, with plans you can review and commits that either apply fully or not at all.**

<p>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/theorigamicorporation/terraform-provider-edgeos/ci.yml?style=flat-square&branch=main&label=CI"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/release.yml"><img alt="Release workflow" src="https://img.shields.io/github/actions/workflow/status/theorigamicorporation/terraform-provider-edgeos/release.yml?style=flat-square&label=release"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://img.shields.io/github/actions/workflow/status/theorigamicorporation/terraform-provider-edgeos/codeql.yml?style=flat-square&branch=main&label=CodeQL"></a>
<a href="https://scorecard.dev/viewer/?uri=github.com/theorigamicorporation/terraform-provider-edgeos"><img alt="OpenSSF Scorecard" src="https://img.shields.io/ossf-scorecard/github.com/theorigamicorporation/terraform-provider-edgeos?style=flat-square&label=OpenSSF%20Scorecard"></a>
</p>
<p>
<a href="https://goreportcard.com/report/github.com/theorigamicorporation/terraform-provider-edgeos"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/theorigamicorporation/terraform-provider-edgeos?style=flat-square"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/actions/workflows/ci.yml"><img alt="Coverage" src="https://img.shields.io/endpoint?style=flat-square&url=https%3A%2F%2Fraw.githubusercontent.com%2Ftheorigamicorporation%2Fterraform-provider-edgeos%2Fbadges%2Fcoverage.json"></a>
<a href=".golangci.yml"><img alt="golangci-lint" src="https://img.shields.io/badge/linted%20with-golangci--lint-00ADD8?style=flat-square&logo=go&logoColor=white"></a>
</p>
<p>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/theorigamicorporation/terraform-provider-edgeos?style=flat-square&sort=semver"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/releases/latest"><img alt="Release date" src="https://img.shields.io/github/release-date/theorigamicorporation/terraform-provider-edgeos?style=flat-square"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/releases"><img alt="Downloads" src="https://img.shields.io/github/downloads/theorigamicorporation/terraform-provider-edgeos/total?style=flat-square"></a>
</p>
<p>
<a href="https://registry.terraform.io/providers/theorigamicorporation/edgeos/latest"><img alt="Terraform Registry" src="https://img.shields.io/badge/Terraform-Registry-7B42BC?style=flat-square&logo=terraform&logoColor=white"></a>
<a href="https://search.opentofu.org/provider/theorigamicorporation/edgeos/latest"><img alt="OpenTofu Registry" src="https://img.shields.io/badge/OpenTofu-Registry-FFDA18?style=flat-square&logo=opentofu&logoColor=black"></a>
</p>
<p>
<a href="LICENSE"><img alt="License MPL-2.0" src="https://img.shields.io/github/license/theorigamicorporation/terraform-provider-edgeos?style=flat-square"></a>
<a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/theorigamicorporation/terraform-provider-edgeos?style=flat-square&logo=go&logoColor=white"></a>
<a href=".github/workflows/ci.yml"><img alt="Terraform >= 1.13" src="https://img.shields.io/badge/Terraform-%3E%3D%201.13-7B42BC?style=flat-square"></a>
<a href=".github/workflows/ci.yml"><img alt="OpenTofu >= 1.11" src="https://img.shields.io/badge/OpenTofu-%3E%3D%201.11-FFDA18?style=flat-square"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/commits/main"><img alt="Last commit" src="https://img.shields.io/github/last-commit/theorigamicorporation/terraform-provider-edgeos?style=flat-square"></a>
<a href="https://github.com/theorigamicorporation/terraform-provider-edgeos/issues"><img alt="Open issues" src="https://img.shields.io/github/issues/theorigamicorporation/terraform-provider-edgeos?style=flat-square"></a>
<a href="CONTRIBUTING.md"><img alt="PRs welcome" src="https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square"></a>
</p>

</div>

## Contents

- [Features](#features)
- [Install](#install)
- [Quick start](#quick-start)
- [Resources and data sources](#resources-and-data-sources)
- [Authentication and connection](#authentication-and-connection)
- [Transport](#transport)
- [Ownership rules](#ownership-rules)
- [Compatibility](#compatibility)
- [Limitations](#limitations)
- [EdgeOS quirks worth knowing](#edgeos-quirks-worth-knowing)
- [FAQ](#faq)
- [Development](#development)
- [Contributing and security](#contributing-and-security)

## Features

- **Atomic commits.** Each resource change is one EdgeOS batch: deletes and sets
  are applied and committed together, and a rejected commit leaves the router
  untouched. The error shows the config path EdgeOS complained about.
- **Same API as the web UI.** No SSH scraping and no custom agent on the router.
- **Drift detection.** Edits made in the web UI or CLI show up in the next plan
  and are reverted on apply.
- **Careful ownership.** A resource only touches the attributes it manages;
  rules below a rule set or VLANs on a port are left alone.
- **Escape hatch.** `edgeos_config` manages any node as a value, a list or a
  JSON subtree, and the `edgeos_config` data source reads anything.
- **Plan-time checks.** Invalid combinations (ports without tcp/udp, NAT rules
  without interfaces, values EdgeOS refuses) fail at plan, before anything is
  sent.
- **One binary for Terraform and OpenTofu**, tested in CI against several
  versions of each.

## Install

<table>
<tr><th>Terraform</th><th>OpenTofu</th></tr>
<tr>
<td>

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

```sh
terraform init
```

</td>
<td>

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

```sh
tofu init
```

</td>
</tr>
</table>

The block is identical. Terraform resolves the address against
`registry.terraform.io`, OpenTofu against `registry.opentofu.org`, and both
download the same signed release.

## Quick start

A firewall for traffic to the router itself:

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
```

Run `terraform plan` or `tofu plan` to see the EdgeOS nodes that will change,
then apply.

<details>
<summary><b>A small home network: VLAN WAN, NAT and DHCP</b></summary>

An EdgeRouter X with the ISP on VLAN 100 of `eth0` and the LAN on `eth1`.

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

</details>

<details>
<summary><b>Anything else with <code>edgeos_config</code></b></summary>

```hcl
# A single value.
resource "edgeos_config" "dns_cache" {
  path  = ["service", "dns", "forwarding", "cache-size"]
  value = "1000"
}

# A multi-valued node, order kept.
resource "edgeos_config" "dns_listen" {
  path   = ["service", "dns", "forwarding", "listen-on"]
  values = ["eth1", "eth1.20"]
}

# A whole subtree as JSON. Values are strings, lists of strings or null.
resource "edgeos_config" "nightly_backup" {
  path = ["system", "task-scheduler", "task", "nightly-backup"]
  tree = jsonencode({
    crontab-spec = "30 3 * * *"
    executable   = { path = "/config/scripts/backup.sh" }
  })
}
```

</details>

A complete office setup with port forwards, a guest VLAN, WireGuard and a
static route is in [`examples/full`](examples/full). Every resource and data
source has its own example under [`examples/`](examples) and a page under
[`docs/`](docs).

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
| [`edgeos_system_login_user_public_key`](docs/resources/system_login_user_public_key.md) | an SSH public key of a login user |
| [`edgeos_service_ssh`](docs/resources/service_ssh.md) | SSH server port, listen addresses, password logins |
| [`edgeos_service_gui`](docs/resources/service_gui.md) | web UI ports, listen addresses, ciphers, certificate |

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
removes the attributes the resource manages. `edgeos_service_ssh` and
`edgeos_service_gui` put their settings back to the defaults, so destroy never
switches SSH or the web UI off.

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

## Compatibility

| EdgeOS | ER-X, ER-X SFP, ER-10X (`e50`) | ER-4, ER-6P, ER-12 (`e300`) | ER Lite, ER PoE (`e100`) | ER-8, ER Pro (`e200`) | ER Infinity (`e1000`) |
|---|---|---|---|---|---|
| 3.0.x | tested | expected | expected | expected | expected |
| 2.0.x | expected | expected | expected | expected | expected |
| 1.10.x | expected | expected | expected | expected | expected |

"Expected" means the web API the provider uses is the same, but nobody has
reported a run yet, and it only applies where Ubiquiti ships that release for
the model. A short issue with your model and `show version` output helps.
UniFi gateways (USG, UDM, UXG) run different software and are not supported.

| Tool | Versions tested in CI |
|---|---|
| Terraform | 1.13, 1.15 and the version in [`.tool-versions`](.tool-versions) |
| OpenTofu | 1.11, 1.12 and the version in [`.tool-versions`](.tool-versions) |

## Limitations

- No dedicated resources yet for the IPv6 firewall (`firewall ipv6-name`),
  firewall groups, zone policies, QoS, VPNs other than WireGuard, DNS
  forwarding and dynamic DNS. Manage them with `edgeos_config`.
- The web API saves every commit to `config.boot`; there is no commit-only
  mode until the SSH transport exists.
- Destroying a named node (a rule set, a VLAN, a DHCP network) deletes it with
  everything below it, including children Terraform does not manage.
- Commits are serialised, so large plans apply one resource at a time.

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

## FAQ

<details>
<summary><b>Why not use a VyOS provider? EdgeOS is a Vyatta fork too.</b></summary>

The config tree looks similar, but the interfaces are not. The VyOS providers
talk to the VyOS HTTP API, which EdgeOS does not have, or drive the CLI over
SSH with VyOS commands. EdgeOS forked from Vyatta long before that API existed;
the only structured interface it offers is the JSON API of its web UI, which is
what this provider uses. Node names and validation also differ (`service nat`,
`port-forward`, `switch0`, the DHCP server layout).

</details>

<details>
<summary><b>Which transport should I use?</b></summary>

`api`, the default and currently the only one. It needs HTTPS access to the
router's web UI and an admin user. An SSH transport is planned for setups where
the web UI is disabled, and for committing without saving.

</details>

<details>
<summary><b>How are passwords and keys handled?</b></summary>

The provider password is only sent to the router's login form over HTTPS and
is never logged. Login user passwords (`edgeos_system_login_user`) are
write-only: they are sent when created or changed, EdgeOS keeps a hash, and the
hash is used to detect changes made elsewhere. WireGuard keys can be given
inline, in which case they are stored in your state (marked sensitive), or as
a path such as `/config/auth/wg0.key` to a file you create on the router, which
keeps the key off your machine. Protect your state either way.

</details>

<details>
<summary><b>What happens if an apply fails halfway?</b></summary>

Each resource is one commit. Resources that were committed before the failure
stay applied and are in the state; the failing one is not changed on the
router. Fix the cause and apply again.

</details>

<details>
<summary><b>Can I adopt an existing router config?</b></summary>

Yes. Every resource supports `terraform import` / `tofu import` and `import`
blocks; the ID format is on each docs page. The `edgeos_config` data source
with `children` lists what exists, for example all firewall rule sets.

</details>

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

[Mozilla Public License 2.0](LICENSE). EdgeRouter, EdgeOS and UniFi are
trademarks of Ubiquiti Inc.; this project is not affiliated with Ubiquiti.
