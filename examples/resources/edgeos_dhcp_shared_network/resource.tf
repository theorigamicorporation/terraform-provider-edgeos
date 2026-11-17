resource "edgeos_dhcp_shared_network" "lan" {
  name          = "LAN"
  description   = "Home network"
  authoritative = true

  subnets = {
    "192.168.1.0/24" = {
      default_router = "192.168.1.1"
      dns_servers    = ["192.168.1.1"]
      domain_name    = "home.arpa"
      lease          = 86400
      ranges = [
        { start = "192.168.1.100", stop = "192.168.1.199" },
      ]
      exclude = ["192.168.1.150"]
    }
  }
}

# A guest network with short leases and a public resolver.
resource "edgeos_dhcp_shared_network" "guest" {
  name = "GUEST"

  subnets = {
    "192.168.20.0/24" = {
      default_router = "192.168.20.1"
      dns_servers    = ["198.51.100.53", "203.0.113.53"]
      lease          = 3600
      ranges = [
        { start = "192.168.20.10", stop = "192.168.20.250" },
      ]
    }
  }
}
