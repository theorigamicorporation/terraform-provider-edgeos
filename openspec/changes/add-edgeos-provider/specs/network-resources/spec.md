## ADDED Requirements

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
