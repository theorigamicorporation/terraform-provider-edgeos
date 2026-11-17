# Update dynamic DNS every five minutes.
resource "edgeos_system_task_scheduler_task" "ddns" {
  name       = "asus-ddns"
  interval   = "5m"
  executable = "/config/scripts/asus-ddns.sh"
}

# Back up the config every night at 03:30.
resource "edgeos_system_task_scheduler_task" "backup" {
  name         = "backup"
  crontab_spec = "30 3 * * *"
  executable   = "/config/scripts/backup.sh"
  arguments    = "--keep 7"
}
