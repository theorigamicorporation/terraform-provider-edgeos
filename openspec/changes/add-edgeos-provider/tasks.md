# Tasks

## 1. Research

- [x] 1.1 Read the EdgeOS web UI code to learn login, CSRF, get/partial/batch semantics and the error format
- [x] 1.2 Read the config templates to learn node types (tag, multi, valueless) for the modelled nodes
- [x] 1.3 Record the transport evaluation in design.md

## 2. Core

- [x] 2.1 `internal/configtree`: paths with CLI quoting, values, leaves, Diff, batch JSON
- [x] 2.2 Fuzz tests for path round trips, the parser, batch JSON and value validation
- [x] 2.3 `internal/client`: Transport interface, serialised Apply, error types
- [x] 2.4 `internal/client/webapi`: login, session renewal, partial reads, batch writes, error parsing, sys_info
- [x] 2.5 `internal/fakeedgeos`: in-memory API with validation, fault injection and a CLI to run it

## 3. Provider

- [x] 3.1 Provider schema and configuration from attributes and `EDGEOS_*` variables
- [x] 3.2 Generic leaf resource (create, read, update, delete, import, ownership)
- [x] 3.3 `edgeos_config` resource and data source, `edgeos_system_info` data source
- [x] 3.4 `edgeos_firewall_name`, `edgeos_firewall_rule`
- [x] 3.5 `edgeos_nat_rule`, `edgeos_port_forward`, `edgeos_port_forward_settings`, `edgeos_static_route`, `edgeos_static_interface_route`
- [x] 3.6 `edgeos_interface_ethernet`, `edgeos_interface_vif`, `edgeos_system`, `edgeos_system_login_user`
- [x] 3.7 `edgeos_dhcp_shared_network`, `edgeos_dhcp_static_mapping`
- [x] 3.8 `edgeos_wireguard_interface`, `edgeos_wireguard_peer`

## 4. Tests

- [x] 4.1 Table-driven mapping tests for every resource
- [x] 4.2 Acceptance tests per resource: create, update, replace, import, drift, destroy, errors
- [x] 4.3 Provider configuration, TLS pinning, auth failure, session expiry, serialised commits
- [x] 4.4 Real router tests behind `EDGEOS_ACC_REAL=1`
- [x] 4.5 Run acceptance tests with terraform and tofu

## 5. Repository

- [x] 5.1 `.tool-versions`, justfile, golangci-lint, goreleaser, registry manifest
- [x] 5.2 CI (lint, unit, fuzz, docs drift, acceptance matrix for both CLIs, coverage gate, release check) and release workflow
- [x] 5.3 README, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, CHANGELOG, templates, CODEOWNERS, dependabot
- [x] 5.4 Docs generated with tfplugindocs, examples for every resource and a full example
- [x] 5.5 CI green on GitHub
