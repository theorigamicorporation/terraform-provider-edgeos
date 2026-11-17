# Add the switch interface and DHCP client options

## Why

The EdgeRouter X bridges its LAN ports through `switch0`, so the LAN address,
its firewall and the port membership could only be managed with
`edgeos_config`. Some ISPs also only hand out a lease when the router sends
their box's client identifier and vendor class, which needs
`dhcp-options client-option`; the interface resources left those lines alone.

## What changes

- New resource `edgeos_interface_switch` for `interfaces switch <name>`:
  description, addresses, MTU, MAC, disabled, firewalls, DHCP options, the
  switch ports and `vlan-aware`. It adopts an existing switch like
  `edgeos_interface_ethernet` adopts a port, and imports by name.
- `dhcp_options` on the ethernet, VLAN and switch interfaces gains
  `client_options` and `global_options` (ordered lists of `dhclient.conf`
  lines). Double quotes fail at plan time with a hint to use hex.
- The fake router validates switch names, switch ports (must exist, one switch
  each), `vlan-aware` and the switch MTU, address and DHCP option nodes.
- Every resource page shows an `import` block example next to the import
  command.

## Impact

- `internal/provider/resource_interface.go`, `internal/fakeedgeos/validate_interfaces.go`
- Docs and examples for all resources (import blocks), README, CHANGELOG
