terraform {
  required_providers {
    edgeos = {
      source = "theorigamicorporation/edgeos"
    }
  }
}

# Credentials can also come from EDGEOS_HOST, EDGEOS_USERNAME and
# EDGEOS_PASSWORD, which keeps the password out of the configuration.
provider "edgeos" {
  host     = "192.168.1.1"
  username = "admin"
  password = var.edgeos_password

  # EdgeRouters ship with a self-signed certificate. Either trust it
  # explicitly with ca_certificate, or skip verification.
  ca_certificate = file("${path.module}/router.pem")
}

variable "edgeos_password" {
  type      = string
  sensitive = true
}
