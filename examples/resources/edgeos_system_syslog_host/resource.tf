resource "edgeos_system_syslog_host" "siem" {
  host = "192.0.2.10"
  facilities = {
    all = "info"
  }
}

# A server on a non-standard port.
resource "edgeos_system_syslog_host" "archive" {
  host = "syslog.example.net:5140"
  facilities = {
    all      = "notice"
    authpriv = "info"
  }
}
