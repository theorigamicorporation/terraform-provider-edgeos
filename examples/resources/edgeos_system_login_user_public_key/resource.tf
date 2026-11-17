variable "alice_password" {
  type      = string
  sensitive = true
}

resource "edgeos_system_login_user" "alice" {
  name               = "alice"
  plaintext_password = var.alice_password
}

# From the public key line "ssh-ed25519 AAAAC3Nza... alice@laptop".
resource "edgeos_system_login_user_public_key" "alice_laptop" {
  user = edgeos_system_login_user.alice.name
  name = "alice@laptop"
  type = "ssh-ed25519"
  key  = "AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
}
