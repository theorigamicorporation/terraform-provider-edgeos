# developer-tooling Specification

## Purpose
TBD - created by archiving change add-edgeos-provider. Update Purpose after archive.

## Requirements

### Requirement: Pinned toolchain
The repository SHALL pin Go, golangci-lint, Terraform, OpenTofu, just and goreleaser in `.tool-versions`, and CI SHALL install the same versions from that file.

#### Scenario: Version bump
- **WHEN** a version in `.tool-versions` changes
- **THEN** CI uses the new version without other edits

### Requirement: justfile entry point
A `justfile` SHALL provide build, install, fmt, lint, test, testacc (Terraform and OpenTofu), testacc-real, cover, docs, docs-check, release-check, fake-server and clean recipes, and CI SHALL call these recipes.

#### Scenario: Real router guard
- **WHEN** `just testacc-real` runs without `EDGEOS_ACC_REAL=1` and `EDGEOS_HOST`
- **THEN** it refuses to run and explains that the tests write to the router

### Requirement: Fake router
An in-memory fake EdgeOS API SHALL implement login, get.json, partial.json, batch.json, data.json and heartbeat.json with set-time and commit-time validation and hooks to inject commit failures, expire sessions and edit the config out of band.

#### Scenario: Trying the examples
- **WHEN** a developer runs `just fake-server`
- **THEN** it prints the `EDGEOS_*` variables needed to point terraform or tofu at it

### Requirement: Quality gates
CI SHALL run lint, unit tests with the race detector, a short fuzz run, acceptance tests on a matrix of Terraform and OpenTofu versions, a docs drift check, a goreleaser config check and a coverage gate of 80 percent over `internal/...`.

#### Scenario: Docs drift
- **WHEN** a schema changes without regenerated docs
- **THEN** the docs job fails and says to run `just docs`
