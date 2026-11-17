# SSH keys, SSH server and web UI settings

## Why

Login users can be managed, but their SSH keys cannot, so key based logins
still need the web UI or the CLI, and rotating a key means editing the router
by hand. The SSH server and the web UI have no resources at all, although
hardening them (key only logins, listen addresses, no old TLS ciphers, an own
certificate) is one of the first things done on a new router.

## What changes

- New resource `edgeos_system_login_user_public_key` for
  `system login user <user> authentication public-keys <name>` with `type`,
  `key` and `options`, imported as `<user>:<name>`. `edgeos_system_login_user`
  keeps leaving `public-keys` alone.
- New singleton `edgeos_service_ssh` for `service ssh`: `port`,
  `listen_addresses`, `protocol_version`, `disable_password_authentication`,
  `disable_host_validation`, `allow_root`. Import ID `ssh`.
- New singleton `edgeos_service_gui` for `service gui`: `http_port`,
  `https_port`, `listen_addresses`, `older_ciphers`, `ca_file`, `cert_file`.
  Import ID `gui`.
- The fake router validates these nodes and reports port clashes and
  incomplete keys at commit.
- Examples include an `import` block next to `import.sh`.

## Impact

- New code in `internal/provider` and `internal/fakeedgeos`, docs and examples.
- No change to existing resources apart from the login user description.
