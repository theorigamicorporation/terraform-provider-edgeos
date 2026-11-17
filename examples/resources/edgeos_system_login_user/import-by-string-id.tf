import {
  to = edgeos_system_login_user.example
  id = "alice"
}

# The password the user already has on the router. It is checked against
# the hash, so the plan after the import is empty when it is right.
resource "edgeos_system_login_user" "example" {
  name               = "alice"
  plaintext_password = var.alice_password
}
