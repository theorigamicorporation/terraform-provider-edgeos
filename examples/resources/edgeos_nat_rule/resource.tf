# Masquerade everything leaving through the WAN port.
resource "edgeos_nat_rule" "masquerade" {
  number             = 5000
  type               = "masquerade"
  description        = "LAN to WAN"
  outbound_interface = "eth0"
}

# Send HTTPS arriving on the WAN address to a server on the LAN.
resource "edgeos_nat_rule" "web" {
  number            = 10
  type              = "destination"
  description       = "HTTPS to the web server"
  inbound_interface = "eth0"
  protocol          = "tcp"

  destination = {
    address = "203.0.113.2"
    port    = "443"
  }

  inside_address = {
    address = "192.168.1.20"
    port    = "8443"
  }
}

# Give the server VLAN its own public address.
resource "edgeos_nat_rule" "servers" {
  number             = 5010
  type               = "source"
  description        = "Server VLAN out through 203.0.113.10"
  outbound_interface = "eth0"

  source = {
    address = "192.168.20.0/24"
  }

  outside_address = {
    address = "203.0.113.10"
  }
}
