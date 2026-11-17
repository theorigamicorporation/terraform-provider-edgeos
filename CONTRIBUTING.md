# Contributing

Thanks for helping. Bug reports with the EdgeOS version and the relevant part
of `show configuration commands` (with addresses and secrets replaced) are the
most useful thing you can send.

## Setup

1. Install the pinned toolchain: `asdf install` (see `.tool-versions`). If you
   do not use asdf, install the same versions of Go, golangci-lint, Terraform,
   OpenTofu, just and goreleaser some other way.
2. Run `just` to see the recipes. CI runs the same ones.

## Day to day

```sh
just fmt                 # gofmt and terraform fmt for the examples
just lint                # golangci-lint
just test                # unit tests with -race
just testacc             # acceptance tests with Terraform and OpenTofu
just testacc-terraform -run TestAccFirewallRule   # one test, one CLI
just cover               # coverage over internal/..., fails below 80%
just docs                # regenerate docs/ after schema or example changes
```

Commit the regenerated `docs/` with your change; CI fails when they drift.

## Tests

- Unit tests live next to the code. Mapping between Terraform models and the
  EdgeOS config tree is table driven; please add a row for every new attribute.
- Acceptance tests (`TestAcc*`) start an in-memory fake EdgeOS API per test
  (`internal/fakeedgeos`). Each resource is expected to cover create, update in
  place, replacement, import, drift, destroy and its error paths. The fake
  validates the same things EdgeOS does for the nodes we manage; when you add
  a resource, register the checks EdgeOS would make in a
  `internal/fakeedgeos/validate_<area>.go` file.
- Run the acceptance tests with both CLIs before you open a PR
  (`just testacc`).

### Real router tests

`just testacc-real` runs `TestAccReal*` against the router in `EDGEOS_HOST`.
**These tests write to the router**: they create, change and delete config
under names starting with `TFACC` and every commit is saved to `config.boot`.
Only run them against a lab router you can reset. They refuse to start unless
`EDGEOS_ACC_REAL=1` is set as well:

```sh
EDGEOS_ACC_REAL=1 EDGEOS_HOST=192.0.2.1 EDGEOS_USERNAME=admin \
EDGEOS_PASSWORD=... EDGEOS_INSECURE=true just testacc-real
```

They never run in CI.

## Trying a local build

```sh
just install
```

prints the `dev_overrides` blocks for `~/.terraformrc` and `~/.tofurc`. With
those in place `terraform plan` and `tofu plan` use your local build. Use
`just fake-server` if you do not want to point it at a real router.

## Pull requests

- Keep changes focused and describe what you tested.
- Fill in the pull request template.
- Examples and tests must use documentation addresses (192.0.2.0/24,
  198.51.100.0/24, 203.0.113.0/24, RFC 1918 ranges) and never real keys, MAC
  addresses or config from your network.
- Add a line to `CHANGELOG.md` under "Unreleased".

## Design notes

The archived OpenSpec change in
`openspec/changes/archive/2026-11-17-add-edgeos-provider/` describes the
transport evaluation, the data model and the ownership rules, and
`openspec/specs/` holds the current requirements. Read `design.md` there before
larger changes, and start new features with an OpenSpec change.
