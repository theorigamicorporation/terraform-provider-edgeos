# Design

## Resource split

`system syslog` has one global section and any number of host and file
targets. Following `edgeos_firewall_name`/`edgeos_firewall_rule`, the global
section is an adopted singleton (`edgeos_system_syslog`, ID `syslog`, path
`system syslog global`) and each target is its own tag resource keyed by the
host (`192.0.2.10`, `syslog.example.net:5140`) or file name. Separate
resources let modules add a remote server without owning the local settings.

`system offload` is small and fixed, so it is one adopted singleton
(`edgeos_system_offload`, ID `offload`) with flat boolean attributes mapped to
`enable`/`disable`. EdgeOS accepts every knob on every model, so the hardware
split (MediaTek `hwnat`, Cavium `ipv4`/`ipv6`, `ipsec` on both) is documented
rather than enforced.

The task scheduler resource is named `edgeos_system_task_scheduler_task`,
derived from the path like `edgeos_system_login_user`.

## Singletons and import

Both singletons only manage the attributes that are set and require at least
one. Import sets the ID to null, as `edgeos_system` does, so the read after an
import picks up every attribute; a shared `singletonImport` helper does this.

## Facilities

Facilities are a `map(string)` of facility to level. The resource owns the
whole facility list (`facility *`), like `ntp_servers` on `edgeos_system`, so
facilities added by hand are removed. Owning a tag whose leaves are still
configured used to delete the tag and skip the unchanged leaves; the leaf
resource now drops such owned flags from the diff (`keepCovered`).

## Task intervals

EdgeOS turns `interval` into a cron entry: a number with `m` (default), `h` or
`d`. Values cron cannot express (60m and more, 24h and more, 32d and more) are
rejected at plan time and by the fake's commit check, which also enforces
exactly one of interval and crontab-spec and an executable path.

## Analytics and crash reports

`system analytics-handler send-analytics-report` and
`system crash-handler send-crash-report` are `true`/`false` leaves. They are
optional attributes on `edgeos_system`, read only when set (or on import), so
existing configurations are unaffected.
