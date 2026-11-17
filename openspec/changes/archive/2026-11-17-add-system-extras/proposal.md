# Add system offload, syslog and task scheduler resources

## Why

Offload, syslog and scheduled tasks are set on nearly every EdgeRouter (hwnat
on the ER-X family, the stock syslog facilities, a DDNS or backup script run
from the task scheduler), but today they can only be managed with
`edgeos_config`. That means no plan-time validation, no hardware notes and
subtree JSON for something that has a handful of well known knobs. The
analytics and crash report switches are also commonly turned off and belong
next to the other `system` settings.

## What changes

- New singleton `edgeos_system_offload` for `system offload`: `hwnat`, `ipsec`
  and the `ipv4_*`/`ipv6_*` knobs, with notes on which hardware each applies to.
- New singleton `edgeos_system_syslog` for `system syslog global`: facility to
  level map and archive settings.
- New tag resources `edgeos_system_syslog_host` (`system syslog host <host>`)
  and `edgeos_system_syslog_file` (`system syslog file <name>`), split from the
  global settings the same way rules are split from rule sets.
- New tag resource `edgeos_system_task_scheduler_task` for
  `system task-scheduler task <name>` with interval or crontab spec, executable
  and arguments, validated at plan time.
- `edgeos_system` gains optional `send_analytics_report` and
  `send_crash_report`; unset leaves the router's value alone, so existing
  configurations plan no changes.
- The generic leaf resource keeps an owned tag (a syslog facility) when the
  plan still configures leaves below it, instead of deleting and not
  re-setting it.
- The fake router validates these nodes like EdgeOS 3.0.1 and ships the stock
  `facility protocols level debug` default.

## Non-goals

- Syslog `console` and `user` targets, TCP transport and message formats.
- Uploading task scripts to `/config/scripts`.
- Rebooting the router after offload changes.
