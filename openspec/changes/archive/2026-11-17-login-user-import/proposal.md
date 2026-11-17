# Import login users without a password diff

## Why

Importing `edgeos_system_login_user` from a real router never gave an empty
plan. The router only has the hash, so `plaintext_password` was null after the
import. The config Terraform and OpenTofu generate (`plaintext_password = null`)
failed validation because the attribute was required, and once the password
was written into the config the plan showed an update that would set the
password again, because state (null) and config differed.

## What changes

- `plaintext_password` becomes an optional write-only attribute. It is never
  stored in the state; version 0 states are upgraded and lose it.
- Each plan checks the configured password against the router's
  `encrypted_password` (`$6$` and `$5$` crypt hashes, with a small in-repo
  implementation in `internal/shacrypt`). A match keeps the hash and plans
  nothing; a mismatch plans an update that sends the password.
- `encrypted_password` becomes optional and computed: a configured hash is
  written as is. Neither attribute set keeps the user's current password,
  but a new user needs one of the two. The two conflict.
- The fake router stores real SHA-512 crypt hashes.

## Impact

- `internal/provider/resource_login_user.go`, its tests, `internal/fakeedgeos`,
  new `internal/shacrypt`, docs and the import example.
- Configs that set `plaintext_password` keep working. Write-only attributes need
  Terraform 1.11 or OpenTofu 1.11, below the versions the provider supports.
