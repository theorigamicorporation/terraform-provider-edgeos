# network-resources Specification

## Purpose
TBD - created by archiving change add-edgeos-provider. Update Purpose after archive.

## Requirements

### Requirement: Owned attributes only
Every modelled resource SHALL only create, change and delete the leaves it owns below its base path and SHALL leave other config below that path untouched.

#### Scenario: Rules are not owned by the rule set
- **WHEN** `edgeos_firewall_name` is updated
- **THEN** rules below the rule set, managed or not, are unchanged

#### Scenario: Physical interfaces
- **WHEN** an `edgeos_interface_ethernet` resource is destroyed
- **THEN** only its managed attributes are removed and the interface and its VLANs stay

### Requirement: Drift detection
Every resource SHALL read its node on refresh, report changes made outside Terraform and correct them on apply. A tag node deleted outside Terraform SHALL be planned for creation again.

#### Scenario: Out-of-band edit
- **WHEN** a managed value is changed in the web UI
- **THEN** the next plan shows an in-place update back to the configured value

### Requirement: Import
Every resource SHALL support import by a readable ID: names, numbers, `<parent>:<key>` pairs, or `eth0.100` for VLAN interfaces.

#### Scenario: Round trip
- **WHEN** a resource is imported by its ID
- **THEN** the imported state matches the state created by Terraform, except for write-only secrets

### Requirement: Plan-time validation
Resources SHALL reject combinations EdgeOS would refuse at commit time when they can be detected from the configuration alone, such as port matches without a tcp or udp protocol, reject-tcp without tcp, or a destination NAT rule without an inbound interface.

#### Scenario: Invalid firewall rule
- **WHEN** a rule matches a destination port with `protocol = "icmp"`
- **THEN** plan fails and nothing is sent to the router

### Requirement: Modelled resources
The provider SHALL include `edgeos_firewall_name`, `edgeos_firewall_rule`, `edgeos_nat_rule`, `edgeos_port_forward`, `edgeos_port_forward_settings`, `edgeos_interface_ethernet`, `edgeos_interface_vif`, `edgeos_dhcp_shared_network`, `edgeos_dhcp_static_mapping`, `edgeos_static_route`, `edgeos_static_interface_route`, `edgeos_wireguard_interface`, `edgeos_wireguard_peer`, `edgeos_system` and `edgeos_system_login_user`.

#### Scenario: Secrets
- **WHEN** a WireGuard private or preshared key is configured inline
- **THEN** it is marked sensitive and never shown in plan output

### Requirement: Global firewall settings
The provider SHALL include `edgeos_firewall_settings`, which manages the options directly below `firewall` (`all-ping`, `broadcast-ping`, `ip-src-route`, `ipv6-src-route`, `ipv6-receive-redirects`, `log-martians`, `receive-redirects`, `send-redirects`, `source-validation`, `syn-cookies`), is imported with the ID `firewall`, and never changes rule sets or other nodes below `firewall`.

#### Scenario: Destroy restores defaults
- **WHEN** an `edgeos_firewall_settings` resource is destroyed
- **THEN** every option it managed is set to the EdgeOS default and the rule sets are unchanged

#### Scenario: Unset options
- **WHEN** an option is not set in the configuration
- **THEN** it is neither read nor changed

### Requirement: UPnP service
The provider SHALL include `edgeos_service_upnp`, which owns `service upnp2` (`listen-on`, `wan`, `nat-pmp`, `secure-mode` and the ACL rules), is imported with the ID `upnp2` and deletes `service upnp2` on destroy.

#### Scenario: Existing UPnP config
- **WHEN** the resource is created while `service upnp2` exists
- **THEN** the apply fails and asks for an import with the ID `upnp2`

#### Scenario: Rule added by hand
- **WHEN** an ACL rule that is not in the configuration is added on the router
- **THEN** the next plan removes it

### Requirement: Global DHCP server settings
The provider SHALL include `edgeos_dhcp_server_settings`, which manages `disabled`, `hostfile-update`, `static-arp`, `use-dnsmasq` and `global-parameters` below `service dhcp-server`, is imported with the ID `dhcp-server`, and never changes shared networks.

#### Scenario: Shared networks are not owned
- **WHEN** an `edgeos_dhcp_server_settings` resource is updated or destroyed
- **THEN** every `shared-network-name` below `service dhcp-server` is unchanged
