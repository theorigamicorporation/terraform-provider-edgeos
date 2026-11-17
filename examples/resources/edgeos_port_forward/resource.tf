resource "edgeos_port_forward_settings" "this" {
  wan_interface  = "eth0"
  lan_interfaces = ["switch0"]
  hairpin_nat    = true
  auto_firewall  = true
}

resource "edgeos_port_forward" "https" {
  number        = 1
  description   = "HTTPS to the reverse proxy"
  original_port = "443"
  protocol      = "tcp"

  forward_to = {
    address = "192.168.1.20"
    port    = "8443"
  }

  depends_on = [edgeos_port_forward_settings.this]
}

resource "edgeos_port_forward" "wireguard" {
  number        = 2
  description   = "WireGuard to the VPN box"
  original_port = "51820"
  protocol      = "udp"

  forward_to = {
    address = "192.168.1.30"
  }

  depends_on = [edgeos_port_forward_settings.this]
}
