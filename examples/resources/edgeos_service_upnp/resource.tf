resource "edgeos_service_upnp" "this" {
  listen_on   = ["switch0"]
  wan         = "eth0.555"
  nat_pmp     = true
  secure_mode = true

  acl_rules = {
    "10" = {
      action        = "allow"
      description   = "LAN hosts, unprivileged ports only"
      subnet        = "192.168.1.0/24"
      external_port = "1024-65535"
      local_port    = "1024-65535"
    }
    "9999" = {
      action = "deny"
    }
  }
}
