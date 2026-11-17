## ADDED Requirements

### Requirement: Login user passwords
`edgeos_system_login_user` SHALL treat `plaintext_password` as write-only and never store it in the state. Each plan SHALL check a configured password against the `$5$`/`$6$` hash on the router and only send it when it does not match. `encrypted_password` SHALL be optional and computed and conflict with `plaintext_password`; a configured hash SHALL be written as is. With neither set, an existing user SHALL keep its password, and creating a user SHALL fail.

#### Scenario: Import with the right password
- **WHEN** an existing user is imported with an import block and the config has its current `plaintext_password`
- **THEN** the plan is empty and nothing is sent to the router

#### Scenario: Import with generated config
- **WHEN** the config was generated with `-generate-config-out`
- **THEN** it validates and the plan is empty

#### Scenario: Wrong password
- **WHEN** the configured password does not match the hash on the router
- **THEN** the plan updates the user, `encrypted_password` is unknown, and the apply sends the password

#### Scenario: Same password set by hand
- **WHEN** the password is set outside Terraform to the configured one with a new salt
- **THEN** the plan is empty

#### Scenario: New user without a password
- **WHEN** a new user has neither `plaintext_password` nor `encrypted_password`
- **THEN** plan fails and nothing is sent to the router
