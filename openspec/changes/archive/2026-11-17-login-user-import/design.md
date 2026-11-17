# Design

## Why write-only

The plan after an import can only be empty when no attribute differs between
the imported state and the config. A normal attribute that the router cannot
return is null in the imported state and set in the config, which is always
an update, whatever the plan modifiers do. A write-only attribute is null in
both, so the comparison has to happen against something the router does
return: the hash.

That also takes the password out of the state, which the old design had to
warn about.

## Checking the password

EdgeOS stores `crypt(3)` hashes, `$6$` (SHA-512) on current versions. Neither
`golang.org/x/crypto` nor the existing dependencies implement SHA-crypt, so
`internal/shacrypt` implements Drepper's algorithm for `$5$` and `$6$`,
including `rounds=`, and is tested with the vectors from the specification
cross-checked with `openssl passwd` and `mkpasswd`. Hashes in other formats
never match, so the password is sent once and the router stores a `$6$`
hash that can be checked from then on.

The check runs in the plan modifier of `encrypted_password`, which reads the
password from the config:

| config | prior state | plan |
|---|---|---|
| `encrypted_password` set | any | the configured hash |
| password matches the hash | exists | keep the hash, no update |
| password differs or is unknown | exists | hash unknown, update sends the password |
| neither | exists | keep the hash |
| neither | none (create) | error |

Create and Update copy the password from the config into the model, as the
plan does not carry write-only values. When the password is sent, Update
drops the old hash from the prior leaves so the batch does not also delete
`encrypted-password`; the commit replaces it.

The resolved hash is owned as a leaf, so a configured hash is written and an
unchanged one is a no-op in the diff.
