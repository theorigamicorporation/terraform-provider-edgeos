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
- DHCP server resources: `edgeos_dhcp_shared_network` and `edgeos_dhcp_static_mapping`.
- WireGuard resources: `edgeos_wireguard_interface` (with the public key derived
  from an inline private key) and `edgeos_wireguard_peer`.
- Data sources: `edgeos_config`, `edgeos_system_info`.
- Fake EdgeOS API server for tests and for trying the examples locally.
