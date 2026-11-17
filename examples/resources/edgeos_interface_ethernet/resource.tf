resource "edgeos_firewall_name" "wan_in" {
  name = "WAN_IN"
}

resource "edgeos_firewall_name" "wan_local" {
  name = "WAN_LOCAL"
}

# The WAN port gets its address from the ISP and is protected by two rule sets.
resource "edgeos_interface_ethernet" "wan" {
  name           = "eth0"
  description    = "ISP uplink"
  addresses      = ["dhcp"]
  firewall_in    = edgeos_firewall_name.wan_in.name
  firewall_local = edgeos_firewall_name.wan_local.name

  dhcp_options = {
    default_route          = true
    default_route_distance = 210
    name_server            = false
  }
}

# A LAN port with static addresses and a fixed link.
resource "edgeos_interface_ethernet" "lan" {
  name        = "eth1"
  description = "LAN"
  addresses   = ["192.168.1.1/24", "2001:db8:1::1/64"]
  duplex      = "full"
  speed       = "1000"
  mtu         = 1500
}

# An unused port, switched off.
resource "edgeos_interface_ethernet" "spare" {
  name     = "eth4"
  disabled = true
}

# An ISP that wants to see its own box: the client identifier and vendor
# class are sent as hex because EdgeOS refuses double quotes in values.
resource "edgeos_interface_ethernet" "fibre" {
  name      = "eth3"
  addresses = ["dhcp"]

  dhcp_options = {
    default_route = true
    client_options = [
      "send dhcp-client-identifier 01:02:00:5e:00:53:01;",
      "send vendor-class-identifier 75:64:68:63:70:20:31:2e:32:35:2e:31;",
    ]
  }
}
