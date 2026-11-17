resource "edgeos_system" "this" {
  host_name    = "gw01"
  domain_name  = "example.net"
  time_zone    = "Europe/Amsterdam"
  name_servers = ["192.0.2.53", "198.51.100.53"]
  ntp_servers  = ["0.ubnt.pool.ntp.org", "192.0.2.123"]
}
