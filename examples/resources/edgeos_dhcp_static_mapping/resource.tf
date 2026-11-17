resource "edgeos_dhcp_shared_network" "lan" {
  name = "LAN"

  subnets = {
    "192.168.1.0/24" = {
      default_router = "192.168.1.1"
      dns_servers    = ["192.168.1.1"]
      ranges = [
        { start = "192.168.1.100", stop = "192.168.1.199" },
      ]
    }
  }
}

resource "edgeos_dhcp_static_mapping" "printer" {
  shared_network = edgeos_dhcp_shared_network.lan.name
  subnet         = "192.168.1.0/24"
  name           = "printer"
  ip_address     = "192.168.1.20"
  mac_address    = "02:00:5e:00:53:20"
}

resource "edgeos_dhcp_static_mapping" "nas" {
  shared_network = edgeos_dhcp_shared_network.lan.name
  subnet         = "192.168.1.0/24"
  name           = "nas"
  ip_address     = "192.168.1.21"
  mac_address    = "02:00:5e:00:53:21"
}
