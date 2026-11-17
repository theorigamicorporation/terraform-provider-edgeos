## ADDED Requirements

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
