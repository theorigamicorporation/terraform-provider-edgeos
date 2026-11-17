resource "edgeos_firewall_name" "wan_in" {
  name           = "WAN_IN"
  description    = "Internet to LAN"
  default_action = "drop"
}

resource "edgeos_firewall_name" "wan_local" {
  name               = "WAN_LOCAL"
  description        = "Internet to the router"
  default_action     = "drop"
  enable_default_log = true
}
