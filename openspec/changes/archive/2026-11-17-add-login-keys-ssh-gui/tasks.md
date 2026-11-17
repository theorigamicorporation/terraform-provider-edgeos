# Tasks

## 1. Public keys

- [x] 1.1 `edgeos_system_login_user_public_key` with type, key, options and `<user>:<name>` import
- [x] 1.2 Plan-time check that the key decodes and its type matches `type`
- [x] 1.3 Refuse to create a key for a user that does not exist
- [x] 1.4 Point the login user docs at the new resource

## 2. Services

- [x] 2.1 `edgeos_service_ssh` with defaults, owned flags and reset on destroy
- [x] 2.2 `edgeos_service_gui` with defaults, port clash check and endpoint warning
- [x] 2.3 Fixed import IDs `ssh` and `gui`

## 3. Fake router

- [x] 3.1 Set-time checks for ports, enums, listen addresses, files and keys
- [x] 3.2 Commit checks for incomplete keys and port clashes
- [x] 3.3 Unit tests for the new checks

## 4. Tests and docs

- [x] 4.1 Mapping, validation and import unit tests
- [x] 4.2 Acceptance tests with import command, import block, drift, replacement and destroy
- [x] 4.3 Examples with `import.sh` and `import-by-string-id.tf`, regenerated docs
- [x] 4.4 README resource list, CHANGELOG
- [x] 4.5 lint, unit and acceptance tests with Terraform and OpenTofu
