# Tasks

## 1. Resources

- [x] 1.1 `edgeos_firewall_settings` with defaults written back on destroy
- [x] 1.2 `edgeos_service_upnp` with ACL rules
- [x] 1.3 `edgeos_dhcp_server_settings`
- [x] 1.4 Register the resources and point the shared network docs at the new DHCP resource

## 2. Fake router

- [x] 2.1 Set and commit checks for the global firewall options and `service upnp2`
- [x] 2.2 Factory firewall options in the default tree

## 3. Tests

- [x] 3.1 Mapping, partial refresh and import ID unit tests
- [x] 3.2 Acceptance tests: create, update, import command, import block, drift, destroy, errors, validation
- [x] 3.3 Run with Terraform and OpenTofu

## 4. Docs

- [x] 4.1 Examples with `import.sh` and `import-by-string-id.tf`, regenerate docs
- [x] 4.2 README resource table and changelog
