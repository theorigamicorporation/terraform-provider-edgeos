# Create the key on the router first, so it never leaves the device:
#   umask 077 && wg genkey > /config/auth/wg0.key
# private_key is needed to create the interface. For an imported interface it
# can be left out, and the interface keeps the key it has.
resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  description = "Road warriors"
  addresses   = ["10.8.0.1/24"]
  listen_port = 51820
  mtu         = 1420
  private_key = "/config/auth/wg0.key"
  firewall_in = edgeos_firewall_name.wg_in.name
}

resource "edgeos_firewall_name" "wg_in" {
  name           = "WG_IN"
  default_action = "accept"
}
