resource "edgeos_static_interface_route" "site_b" {
  destination = "10.66.0.0/24"

  next_hop_interfaces = {
    wg0 = {
      description = "site to site tunnel"
    }
  }
}
