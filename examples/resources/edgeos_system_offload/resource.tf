# EdgeRouter X family (MediaTek): hardware NAT.
resource "edgeos_system_offload" "this" {
  hwnat = true
}

# On a Cavium model (EdgeRouter Lite, 4, 6P, 12, ...) use the ipv4/ipv6 knobs
# instead:
#
# resource "edgeos_system_offload" "this" {
#   ipv4_forwarding = true
#   ipv4_vlan       = true
#   ipv4_pppoe      = true
#   ipv6_forwarding = true
#   ipv6_vlan       = true
#   ipsec           = true
# }
