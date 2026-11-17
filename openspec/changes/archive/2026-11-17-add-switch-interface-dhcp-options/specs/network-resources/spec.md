## ADDED Requirements

### Requirement: Switch interfaces
The provider SHALL include `edgeos_interface_switch` for `interfaces switch <name>`, with the attributes shared by the other interface resources plus the switch ports and `vlan-aware`. It SHALL take over an existing switch, create a missing one, and on destroy remove only the settings it manages.

#### Scenario: Switch ports
- **WHEN** a port is removed from `switch_ports`
- **THEN** it is taken out of the switch, and ports that stay keep their per-port settings

#### Scenario: Unknown port
- **WHEN** `switch_ports` names a port the router does not have
- **THEN** the commit fails and nothing changes

### Requirement: DHCP client options
The `dhcp_options` block of the ethernet, VLAN and switch interfaces SHALL manage `client-option` and `global-option` as ordered lists. Values with double quotes SHALL fail at plan time with a hint to write strings as hex.

#### Scenario: Unset list on a port
- **WHEN** `client_options` is not set on `edgeos_interface_ethernet`
- **THEN** client option lines on the port are neither read nor changed

#### Scenario: Quoted value
- **WHEN** a client option contains `"`
- **THEN** plan fails, the error shows the hex form, and nothing is sent to the router

### Requirement: Import blocks
Every resource page SHALL show an `import` block example in addition to the import command.

#### Scenario: Import block
- **WHEN** a resource is imported with an `import` block using the documented ID
- **THEN** the plan imports it without further changes
