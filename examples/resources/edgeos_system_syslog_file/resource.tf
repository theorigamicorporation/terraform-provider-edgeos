# Kernel messages (firewall log rules) to /var/log/user/firewall.
resource "edgeos_system_syslog_file" "firewall" {
  name = "firewall"
  facilities = {
    kern = "warning"
  }
  archive_files = 3
  archive_size  = 512
}
