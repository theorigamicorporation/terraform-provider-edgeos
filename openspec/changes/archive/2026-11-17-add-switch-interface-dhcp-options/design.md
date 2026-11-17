# Design

## Switch ownership

`switch0` is part of the factory config of an EdgeRouter X and usually carries
the LAN the user reaches the router through, so the resource follows
`edgeos_interface_ethernet` rather than `edgeos_interface_vif`: only the
attributes that are set are read and managed, import reads all of them, and
destroy removes the managed settings but keeps the switch. Unlike a physical
port the switch may be missing (it was deleted, or the model has none), so it
uses `Adopt` instead of `MustExist`: create makes the node when it is not there.

`switch_ports` owns the whole `switch-port interface` list, like
`ntp_servers` on `edgeos_system`: ports not in the set are removed with their
per-port settings, ports that stay keep them (per-port `vlan pvid`/`vid` are not
modelled). `vlan_aware` maps to `enable` / `disable`.

`disabled` and `mac` are shared with the other interfaces through
`ifaceAttributes`, so the switch gets the same attribute set without new
builders.

## DHCP client options

`client-option` and `global-option` are multi-valued nodes whose values EdgeOS
copies into `dhclient.conf` in order, so they are lists, not sets. They follow
the existing rule for optional attributes: on the adopting resources (ethernet,
switch) an unset list is not read and lines added by hand stay; on
`edgeos_interface_vif`, which owns its node, the lists are read like every other
attribute. An empty list is refused so that unset and empty cannot differ.

EdgeOS refuses `"` in any value. dhclient accepts strings as colon separated
hex, so the validator rejects quotes with that hint, e.g.
`send vendor-class-identifier 75:64:68:63:70:20:31:2e:32:35:2e:31;` for
`"udhcp 1.25.1"`.

## Import blocks

tfplugindocs renders `examples/resources/<name>/import-by-string-id.tf` in the
default resource template, so no template change is needed. Acceptance tests
use `ImportStateKind: resource.ImportBlockWithID` next to the import command
steps.
