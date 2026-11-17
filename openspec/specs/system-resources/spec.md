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
