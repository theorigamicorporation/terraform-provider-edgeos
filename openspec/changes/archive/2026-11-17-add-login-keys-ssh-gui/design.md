# Design

## Keys as their own resource

Keys are rotated independently of the user, often by different people, and a
list attribute on the user would make every key change a diff of the whole
user. A separate tag resource below the user fits the existing generic leaf
resource: create fails if the key exists, destroy deletes the key node only.

Create checks that the user exists first. EdgeOS would otherwise create the
user implicitly, without a password, which is never what a key resource
should do. The user resource does not model keys and keeps ignoring
`public-keys` on update, so both resources can be used together; destroying
the user still removes its keys with it.

Plan-time validation decodes the base64 key and compares the type encoded in
the blob with `type`, which catches the most common mistake (pasting an RSA
key into an ed25519 resource). `options` goes through the generic value
validator, so options with double quotes such as `from="..."` are rejected:
EdgeOS cannot store double quotes.

## Singletons own all modelled attributes

`edgeos_system` and `edgeos_port_forward_settings` only manage the attributes
that are set. For the SSH server and the web UI that would make the
configuration ambiguous for security relevant flags: leaving out
`disable_password_authentication` would not say whether it is on. Both
resources therefore own every modelled attribute and give each a default that
matches a new router (port 22 and `v2`; ports 80/443 and `older-ciphers
enable`). Options the resources do not model are left alone.

Both use `Adopt`: create takes over the existing node, or creates it when SSH
was switched off. Delete uses `Reset` to write the defaults back for port,
protocol and ciphers and deletes the other owned leaves, so the node always
keeps at least one leaf and destroy never removes `service ssh` or
`service gui`. Switching SSH off has to be done explicitly with
`edgeos_config` or by hand.

The import ID is the fixed string `ssh` or `gui`, as with `system` and
`port-forward`.

## The provider talks to the web UI

Changing `https_port` or `listen_addresses` of `service gui` moves the
endpoint the provider uses. The resource warns at plan time when either
changes, and the docs say to apply such a change on its own and update the
provider block afterwards. The read after the commit may fail on a real
router; the next run with the new provider settings converges.

## Fake router

`validate_access.go` adds the template checks (port ranges, enums for
`protocol-version`, `older-ciphers` and the key type, IP listen addresses,
absolute file names, base64 keys) and commit checks for incomplete keys,
equal web UI ports and an SSH port that clashes with the web UI. The default
tree gets `older-ciphers enable` like a factory config.

`allow-root` follows the Vyatta template EdgeOS inherited; it has not been
checked on a 3.0.1 router yet. With the default `false` nothing is ever sent
for it.
