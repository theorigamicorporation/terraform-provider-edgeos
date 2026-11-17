# Design

## Names and IDs

The singletons follow `edgeos_port_forward_settings`: `<area>_settings` and an
ID that is the literal node name (`firewall`, `dhcp-server`). UPnP is a service
that is either configured or not, so it is named after the service
(`edgeos_service_upnp`) with the ID `upnp2`, the EdgeOS node name.

## Ownership

`edgeos_firewall_settings` and `edgeos_dhcp_server_settings` use the generic
leaf resource with `Adopt`: create takes over the node, only attributes that
are set are read and written, and import reads everything (an all-null model
only exists right after import because validation needs one attribute). Their
leaves are plain values, so the rule sets and shared networks below the same
base path are never in the owned set.

The firewall leaves always have a value on EdgeOS (a factory `config.boot`
lists all of them), so destroy uses `Reset` to write the defaults back instead
of deleting the leaves. Removing an attribute from the configuration deletes
the leaf, which also gives the default. The DHCP leaves are absent on many
routers, so destroy deletes them and an empty `service dhcp-server` node is
pruned, as EdgeOS does.

`edgeos_service_upnp` is a plain tag-like resource: it reads every modelled
attribute, an existing node has to be imported, and destroy deletes
`service upnp2`, which is the factory state. ACL rules are a map keyed by rule
number; every rule node is owned, so rules added by hand show up as drift.
`port` and `bit-rate` are not modelled and are left alone until destroy.

## Fake router

`validate_firewall_upnp.go` registers the enable/disable enums, the
`source-validation` enum, `listen-on` as a multi node, the upnp2 ACL rule
number, subnet and port range checks, and the commit checks for `listen-on`,
`wan` and the rule `action`. The DHCP global enums already existed.
