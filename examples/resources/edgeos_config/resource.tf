# A single value.
resource "edgeos_config" "dns_cache" {
  path  = ["service", "dns", "forwarding", "cache-size"]
  value = "1000"
}

# A multi-valued leaf; the order is kept.
resource "edgeos_config" "dns_listen" {
  path   = ["service", "dns", "forwarding", "listen-on"]
  values = ["switch0", "eth1.20"]
}

# A valueless node: without value, values or tree the resource only makes
# sure the node exists. This one switches off port eth4.
resource "edgeos_config" "eth4_disabled" {
  path = ["interfaces", "ethernet", "eth4", "disable"]
}

# A whole subtree. Values are strings, lists of strings or null.
resource "edgeos_config" "nightly_backup" {
  path = ["system", "task-scheduler", "task", "nightly-backup"]
  tree = jsonencode({
    crontab-spec = "30 3 * * *"
    executable = {
      path      = "/config/scripts/backup.sh"
      arguments = "--quiet"
    }
  })
}
