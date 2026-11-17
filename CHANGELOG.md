# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[semantic versioning](https://semver.org/).

## Unreleased

First release.

### Added

- Provider configuration for host, port, credentials, TLS verification or a
  pinned certificate, timeout, transport and save behaviour, all settable from
  `EDGEOS_*` environment variables.
- Web UI API transport with session renewal, atomic commits per change and
  serialised writes.
- Resources: `edgeos_config`, `edgeos_firewall_name`, `edgeos_firewall_rule`.
- Data sources: `edgeos_config`, `edgeos_system_info`.
- Fake EdgeOS API server for tests and for trying the examples locally.
