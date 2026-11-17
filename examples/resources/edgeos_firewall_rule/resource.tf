resource "edgeos_firewall_name" "wan_in" {
  name = "WAN_IN"
}

resource "edgeos_firewall_rule" "established" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 10
  action      = "accept"
  description = "Allow established and related"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "invalid" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 20
  action      = "drop"
  description = "Drop invalid state"
  state = {
    invalid = true
  }
}

resource "edgeos_firewall_rule" "web" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 30
  action      = "accept"
  description = "HTTPS to the web server"
  protocol    = "tcp"
  destination = {
    address = "192.168.1.10"
    port    = "443"
  }
}

resource "edgeos_firewall_rule" "ping" {
  ruleset  = edgeos_firewall_name.wan_in.name
  number   = 40
  action   = "accept"
  protocol = "icmp"
  log      = true
  source = {
    address = "198.51.100.0/24"
  }
  icmp = {
    type_name = "echo-request"
  }
}
