# management-access Specification

## Purpose
SSH keys of login users and the settings of the SSH server and the web UI, the services used to reach and manage the router.

## Requirements

### Requirement: Login user public keys
`edgeos_system_login_user_public_key` SHALL manage one key below `system login user <user> authentication public-keys <name>` with `type`, `key` and `options`, SHALL be imported as `<user>:<name>`, and SHALL only delete its own key on destroy. `edgeos_system_login_user` SHALL keep leaving `public-keys` alone on update.

#### Scenario: Rotation
- **WHEN** `key` or `type` changes
- **THEN** the key is updated in place and the user is not changed

#### Scenario: Missing user
- **WHEN** the user does not exist at create time
- **THEN** create fails and no user is created

#### Scenario: Wrong key type
- **WHEN** `type` is `ssh-rsa` but the key encodes `ssh-ed25519`
- **THEN** plan fails and nothing is sent to the router

#### Scenario: Pasted key line
- **WHEN** `key` contains the type, a space or a comment
- **THEN** plan fails asking for the base64 part only

### Requirement: SSH server settings
`edgeos_service_ssh` SHALL manage `service ssh` as a singleton with ID `ssh`, owning `port`, `listen_addresses`, `protocol_version`, `disable_password_authentication`, `disable_host_validation` and `allow_root` with defaults matching a new router, and SHALL leave other options alone.

#### Scenario: Destroy keeps SSH on
- **WHEN** the resource is destroyed
- **THEN** `service ssh` still exists with port 22 and protocol `v2`, and the flags and listen addresses are removed

#### Scenario: SSH switched off by hand
- **WHEN** `service ssh` is deleted outside Terraform
- **THEN** the next apply creates it again with the configured settings

### Requirement: Web UI settings
`edgeos_service_gui` SHALL manage `service gui` as a singleton with ID `gui`, owning `http_port`, `https_port`, `listen_addresses`, `older_ciphers`, `ca_file` and `cert_file`, and SHALL restore ports 80 and 443 and `older-ciphers enable` on destroy.

#### Scenario: Same port twice
- **WHEN** `http_port` equals `https_port`, counting defaults
- **THEN** plan fails

#### Scenario: Provider endpoint moves
- **WHEN** a plan changes `https_port` or `listen_addresses`
- **THEN** the plan shows a warning that the provider's host and port have to follow

### Requirement: Singleton import
The singletons SHALL be importable with the import command and with an `import` block using their fixed ID, and the import SHALL read every owned attribute.

#### Scenario: Import block
- **WHEN** `import { to = edgeos_service_ssh.this, id = "ssh" }` is planned with a matching configuration
- **THEN** the plan imports the resource without changes
