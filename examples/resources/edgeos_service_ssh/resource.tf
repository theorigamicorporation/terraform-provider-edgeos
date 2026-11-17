# Add a working key before turning password logins off.
resource "edgeos_service_ssh" "this" {
  port                            = 22
  listen_addresses                = ["192.168.1.1"]
  disable_password_authentication = true
  disable_host_validation         = true

  depends_on = [edgeos_system_login_user_public_key.alice_laptop]
}
