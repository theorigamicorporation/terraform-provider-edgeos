# A small office router on an EdgeRouter X:
#
#   eth0.100  WAN, DHCP from the ISP on VLAN 100
#   eth1      LAN 192.168.1.0/24
#   eth1.20   guest network 192.168.20.0/24
#   wg0       WireGuard for road warriors, 10.8.0.0/24
#
# All addresses are documentation or private ranges; adjust them to yours.

terraform {
  required_version = ">= 1.5"
  required_providers {
    edgeos = {
      source  = "theorigamicorporation/edgeos"
      version = "~> 0.1"
    }
  }
}

# Connection settings come from EDGEOS_HOST, EDGEOS_USERNAME, EDGEOS_PASSWORD
# and EDGEOS_INSECURE (or EDGEOS_CA_CERTIFICATE).
provider "edgeos" {}

locals {
  wan = "eth0.100"
  lan = "eth1"
}

resource "edgeos_system" "this" {
  host_name    = "gw01"
  domain_name  = "office.example.net"
  time_zone    = "Europe/Amsterdam"
  name_servers = ["192.0.2.53", "198.51.100.53"]
  ntp_servers  = ["0.ubnt.pool.ntp.org", "1.ubnt.pool.ntp.org"]
}

# Firewall

resource "edgeos_firewall_name" "wan_in" {
  name        = "WAN_IN"
  description = "Internet to LAN"
}

resource "edgeos_firewall_name" "wan_local" {
  name        = "WAN_LOCAL"
  description = "Internet to the router"
}

resource "edgeos_firewall_name" "guest_in" {
  name           = "GUEST_IN"
  description    = "Guests may reach the internet only"
  default_action = "accept"
}

resource "edgeos_firewall_rule" "established" {
  for_each = toset([edgeos_firewall_name.wan_in.name, edgeos_firewall_name.wan_local.name])

  ruleset     = each.value
  number      = 10
  action      = "accept"
  description = "Allow established and related"
  state = {
    established = true
    related     = true
  }
}

resource "edgeos_firewall_rule" "invalid" {
  for_each = toset([edgeos_firewall_name.wan_in.name, edgeos_firewall_name.wan_local.name])

  ruleset     = each.value
  number      = 20
  action      = "drop"
  description = "Drop invalid state"
  state = {
    invalid = true
  }
}

resource "edgeos_firewall_rule" "wireguard" {
  ruleset     = edgeos_firewall_name.wan_local.name
  number      = 30
  action      = "accept"
  description = "WireGuard"
  protocol    = "udp"
  destination = {
    port = "51820"
  }
}

resource "edgeos_firewall_rule" "web" {
  ruleset     = edgeos_firewall_name.wan_in.name
  number      = 40
  action      = "accept"
  description = "Port forward to the web server"
  protocol    = "tcp"
  destination = {
    address = "192.168.1.20"
    port    = "443"
  }
}

resource "edgeos_firewall_rule" "guest_no_lan" {
  ruleset     = edgeos_firewall_name.guest_in.name
  number      = 10
  action      = "drop"
  description = "No access from guests to the LAN"
  destination = {
    address = "192.168.1.0/24"
  }
}

# Interfaces

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

resource "edgeos_interface_ethernet" "lan" {
  name        = local.lan
  description = "LAN"
  addresses   = ["192.168.1.1/24"]
}

resource "edgeos_interface_vif" "guest" {
  interface   = local.lan
  vlan        = 20
  description = "Guests"
  addresses   = ["192.168.20.1/24"]
  firewall_in = edgeos_firewall_name.guest_in.name
}

# NAT and port forwards

resource "edgeos_nat_rule" "masquerade" {
  number             = 5000
  type               = "masquerade"
  description        = "Everything out through the WAN"
  outbound_interface = local.wan

  depends_on = [edgeos_interface_vif.wan]
}

resource "edgeos_port_forward_settings" "this" {
  wan_interface  = local.wan
  lan_interfaces = [local.lan]
  hairpin_nat    = true
  auto_firewall  = false

  depends_on = [edgeos_interface_vif.wan]
}

resource "edgeos_port_forward" "https" {
  number        = 1
  description   = "HTTPS to the web server"
  original_port = "443"
  protocol      = "tcp"

  forward_to = {
    address = "192.168.1.20"
  }

  depends_on = [edgeos_port_forward_settings.this]
}

# DHCP

resource "edgeos_dhcp_shared_network" "lan" {
  name          = "LAN"
  authoritative = true

  subnets = {
    "192.168.1.0/24" = {
      default_router = "192.168.1.1"
      dns_servers    = ["192.168.1.1"]
      domain_name    = "office.example.net"
      lease          = 86400
      ranges = [
        { start = "192.168.1.100", stop = "192.168.1.199" },
      ]
    }
  }
}

resource "edgeos_dhcp_static_mapping" "web" {
  shared_network = edgeos_dhcp_shared_network.lan.name
  subnet         = "192.168.1.0/24"
  name           = "web"
  ip_address     = "192.168.1.20"
  mac_address    = "02:00:5e:00:53:20"
}

resource "edgeos_dhcp_shared_network" "guest" {
  name = "GUEST"

  subnets = {
    "192.168.20.0/24" = {
      default_router = "192.168.20.1"
      dns_servers    = ["198.51.100.53"]
      lease          = 3600
      ranges = [
        { start = "192.168.20.10", stop = "192.168.20.250" },
      ]
    }
  }
}

# WireGuard. Create the key on the router so it never leaves it:
#   umask 077 && wg genkey > /config/auth/wg0.key

resource "edgeos_wireguard_interface" "wg0" {
  name              = "wg0"
  description       = "Road warriors"
  addresses         = ["10.8.0.1/24"]
  listen_port       = 51820
  mtu               = 1420
  private_key       = "/config/auth/wg0.key"
  route_allowed_ips = true
}

resource "edgeos_wireguard_peer" "laptop" {
  interface   = edgeos_wireguard_interface.wg0.name
  public_key  = var.laptop_public_key
  description = "laptop"
  allowed_ips = ["10.8.0.2/32"]
}

variable "laptop_public_key" {
  description = "Output of `wg pubkey` on the laptop."
  type        = string
  default     = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
}

# A route to a branch office behind another router on the LAN.
resource "edgeos_static_route" "branch" {
  destination = "198.51.100.0/24"
  next_hops = {
    "192.168.1.254" = { description = "branch router" }
  }
}

data "edgeos_system_info" "router" {}

output "firmware" {
  value = data.edgeos_system_info.router.version
}
