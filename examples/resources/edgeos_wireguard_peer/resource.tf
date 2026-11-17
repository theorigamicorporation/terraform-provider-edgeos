resource "edgeos_wireguard_interface" "wg0" {
  name        = "wg0"
  addresses   = ["10.8.0.1/24"]
  listen_port = 51820
  private_key = "/config/auth/wg0.key"
}

# A laptop that connects to the router. Replace public_key with the output of
# `wg pubkey` on the laptop.
resource "edgeos_wireguard_peer" "laptop" {
  interface     = edgeos_wireguard_interface.wg0.name
  public_key    = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
  description   = "laptop"
  allowed_ips   = ["10.8.0.2/32"]
  preshared_key = "/config/auth/wg0-laptop.psk"
}

# A remote site the router connects to.
resource "edgeos_wireguard_peer" "office" {
  interface            = edgeos_wireguard_interface.wg0.name
  public_key           = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
  description          = "office"
  allowed_ips          = ["10.8.0.3/32", "192.168.50.0/24"]
  endpoint             = "vpn.example.com:51820"
  persistent_keepalive = 25
}
