resource "edgeos_firewall_settings" "this" {
  all_ping               = true
  broadcast_ping         = false
  ip_src_route           = false
  ipv6_receive_redirects = false
  ipv6_src_route         = false
  log_martians           = true
  receive_redirects      = false
  send_redirects         = true
  source_validation      = "disable"
  syn_cookies            = true
}
