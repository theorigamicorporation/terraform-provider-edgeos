# Add resources for the global firewall, UPnP and DHCP server settings

## Why

Three parts of a typical EdgeRouter config still need `edgeos_config`: the
global firewall options (`firewall all-ping`, `syn-cookies`,
`source-validation`, ...), UPnP (`service upnp2`) and the global DHCP server
options (`service dhcp-server use-dnsmasq`, `hostfile-update`, ...).
`edgeos_config` works, but it has no validation, no import by a readable ID and
no attribute level drift, and a subtree config for `firewall` or
`service dhcp-server` would fight with the resources that own the rule sets and
shared networks below them.

## What changes

- `edgeos_firewall_settings`: singleton for the enable/disable leaves directly
  below `firewall` plus `source-validation`. Adopts the existing values, only
  manages attributes that are set, never touches `firewall name` or other
  rule sets, and writes the EdgeOS defaults back on destroy. Import ID
  `firewall`.
- `edgeos_service_upnp`: owns `service upnp2` (`listen-on`, `wan`, `nat-pmp`,
  `secure-mode` and `acl rule <n>`). Create fails on an existing node, destroy
  deletes the node. Import ID `upnp2`.
- `edgeos_dhcp_server_settings`: singleton for `disabled`, `hostfile-update`,
  `static-arp`, `use-dnsmasq` and `global-parameters` below
  `service dhcp-server`. Never touches `shared-network-name`. Import ID
  `dhcp-server`.
- The fake router validates these nodes like EdgeOS 3.0.1 and its default
  config carries the factory firewall options.
- Examples with `import.sh` and an `import` block, generated docs, README and
  changelog entries.

Conntrack options are not part of this change: they live under
`system conntrack`, not `firewall`.
