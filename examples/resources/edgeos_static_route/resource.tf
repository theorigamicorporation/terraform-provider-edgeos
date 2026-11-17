# A branch office reachable through two routers, the second one as backup.
resource "edgeos_static_route" "branch" {
  destination = "198.51.100.0/24"

  next_hops = {
    "192.168.1.254" = {
      description = "primary link"
    }
    "192.168.2.254" = {
      description = "backup link"
      distance    = 10
    }
  }
}

# Never send RFC 1918 traffic to the internet, even when the VPN is down.
resource "edgeos_static_route" "no_leak" {
  destination = "10.0.0.0/8"

  blackhole = {
    description = "drop private traffic without a better route"
    distance    = 254
  }
}
