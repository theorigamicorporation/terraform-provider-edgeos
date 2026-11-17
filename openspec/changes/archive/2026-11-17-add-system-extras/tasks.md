# Tasks

## 1. Fake router

- [x] 1.1 Validate `system offload`, `system syslog`, `system task-scheduler` and the report switches in `internal/fakeedgeos/validate_system.go`
- [x] 1.2 Add the stock `facility protocols level debug` to the default tree

## 2. Resources

- [x] 2.1 `edgeos_system_offload`
- [x] 2.2 `edgeos_system_syslog`, `edgeos_system_syslog_host`, `edgeos_system_syslog_file`
- [x] 2.3 `edgeos_system_task_scheduler_task` with interval and crontab validation
- [x] 2.4 `send_analytics_report` and `send_crash_report` on `edgeos_system`
- [x] 2.5 Keep owned tags whose leaves are still configured

## 3. Tests and docs

- [x] 3.1 Unit tests for mappings, validators and the fake checks
- [x] 3.2 Acceptance tests with import, import blocks, drift, replacement and errors
- [x] 3.3 Examples with `import.sh` and `import-by-string-id.tf`, regenerated docs
- [x] 3.4 README resource list and CHANGELOG
