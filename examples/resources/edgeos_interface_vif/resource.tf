resource "edgeos_firewall_name" "wan_in" {
  name = "WAN_IN"
}

resource "edgeos_firewall_name" "wan_local" {
  name = "WAN_LOCAL"
}

# A VLAN WAN, as many ISPs need it: DHCP on VLAN 100 of eth0.
resource "edgeos_interface_vif" "wan" {
  interface      = "eth0"
  vlan           = 100
  description    = "ISP WAN"
  addresses      = ["dhcp"]
  firewall_in    = edgeos_firewall_name.wan_in.name
  firewall_local = edgeos_firewall_name.wan_local.name

  dhcp_options = {
    default_route          = true
    default_route_distance = 210
    name_server            = false
  }
}

# A guest network on VLAN 20 of the LAN port.
resource "edgeos_interface_vif" "guest" {
  interface   = "eth1"
  vlan        = 20
  description = "Guests"
  addresses   = ["192.168.20.1/24"]
}
