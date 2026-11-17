# Tasks

## 1. DHCP client options

- [x] 1.1 `client_options` / `global_options` in `dhcp_options`, mapped to `client-option` / `global-option`
- [x] 1.2 Validator that rejects double quotes with a hex hint
- [x] 1.3 Unit rows and an acceptance test for order, drift, removal and unmanaged lines

## 2. Switch interface

- [x] 2.1 `edgeos_interface_switch` with the shared interface attributes, `switch_ports` and `vlan_aware`
- [x] 2.2 Import by name, adopt on create, keep the switch on destroy
- [x] 2.3 Fake router checks for switch names, ports and values
- [x] 2.4 Acceptance tests: adopt, update, import command and block, drift, create, replace, errors, validation

## 3. Docs

- [x] 3.1 `import-by-string-id.tf` for every resource
- [x] 3.2 Examples, README, CHANGELOG, regenerated docs
