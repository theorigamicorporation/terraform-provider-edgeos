variable "alice_password" {
  type      = string
  sensitive = true
}

resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  full_name          = "Alice Example"
  level              = "operator"
  plaintext_password = var.alice_password
}
