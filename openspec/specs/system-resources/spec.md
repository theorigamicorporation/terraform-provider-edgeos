# system-resources Specification

## Purpose
TBD - created by archiving change add-system-extras. Update Purpose after archive.

## Requirements

### Requirement: Offload settings
`edgeos_system_offload` SHALL manage `system offload` as a singleton with ID `offload`, mapping `hwnat`, `ipsec`, `ipv4_forwarding`, `ipv4_vlan`, `ipv4_pppoe`, `ipv4_gre`, `ipv4_bonding`, `ipv6_forwarding`, `ipv6_vlan`, `ipv6_pppoe` and `ipv6_bonding` to `enable`/`disable`, and SHALL only read and change the attributes that are set.

#### Scenario: ER-X hardware NAT
- **WHEN** `hwnat = true` is applied to a router without `system offload`
- **THEN** the router has `system offload hwnat enable` and nothing else below `system offload`

#### Scenario: Unmanaged knob
- **WHEN** `system offload ipv4 forwarding` is set outside Terraform and the resource only sets `hwnat`
- **THEN** updates and destroy leave it alone

### Requirement: Syslog settings
`edgeos_system_syslog` SHALL manage `system syslog global` as a singleton with ID `syslog`: a facility to level map that owns the whole facility list, plus archive files and size. Remote hosts and files SHALL be separate resources, `edgeos_system_syslog_host` keyed by host (optionally with `:port`) and `edgeos_system_syslog_file` keyed by file name, each owning its facility list.

#### Scenario: Adopting the defaults
- **WHEN** the resource is created with `{ all = "notice", protocols = "debug" }` on a stock router
- **THEN** nothing is committed and the next plan is empty

#### Scenario: Facility added by hand
- **WHEN** a facility is added outside Terraform
- **THEN** the next apply removes it and keeps the configured facilities and levels

### Requirement: Scheduled tasks
`edgeos_system_task_scheduler_task` SHALL manage `system task-scheduler task <name>` with exactly one of `interval` and `crontab_spec`, an absolute `executable` path and optional `arguments`, imported by task name.

#### Scenario: Interval validation
- **WHEN** `interval = "60m"` is configured
- **THEN** plan fails asking for minutes below 60, and nothing is sent to the router

#### Scenario: Switching schedule type
- **WHEN** `interval` is replaced by `crontab_spec`
- **THEN** one commit deletes the interval and sets the crontab spec

### Requirement: Report switches
`edgeos_system` SHALL offer optional `send_analytics_report` and `send_crash_report`, written as `true`/`false`, which are not read or changed while unset.

#### Scenario: Existing configuration
- **WHEN** a configuration written before these attributes existed is planned
- **THEN** the plan shows no changes

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
