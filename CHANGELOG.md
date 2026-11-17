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
- NAT and routing resources: `edgeos_nat_rule`, `edgeos_port_forward`,
  `edgeos_port_forward_settings`, `edgeos_static_route`,
  `edgeos_static_interface_route`.
- Interface and system resources: `edgeos_interface_ethernet`,
  `edgeos_interface_vif`, `edgeos_system` and `edgeos_system_login_user`.
- DHCP server resources: `edgeos_dhcp_shared_network` and `edgeos_dhcp_static_mapping`.
- WireGuard resources: `edgeos_wireguard_interface` (with the public key derived
  from an inline private key) and `edgeos_wireguard_peer`.
- Data sources: `edgeos_config`, `edgeos_system_info`.
- Fake EdgeOS API server for tests and for trying the examples locally.
- `edgeos_system_login_user_public_key` for SSH keys of login users, managed
  apart from the user so they can be rotated on their own.
- `edgeos_service_ssh` and `edgeos_service_gui` for the SSH server and the web
  UI. Destroy restores the defaults and never switches SSH off.
- `edgeos_firewall_settings` for the global firewall options (`all-ping`,
  redirects, `source-validation`, `syn-cookies`, ...). Destroy writes the
  EdgeOS defaults back.
- `edgeos_service_upnp` for `service upnp2`, including ACL rules.
- `edgeos_dhcp_server_settings` for the global `service dhcp-server` options
  and `global-parameters`.
