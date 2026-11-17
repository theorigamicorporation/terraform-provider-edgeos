# Changing https_port or listen_addresses moves the endpoint the provider
# connects to. Apply that on its own, then update the provider block.
resource "edgeos_service_gui" "this" {
  http_port        = 80
  https_port       = 443
  listen_addresses = ["192.168.1.1"]
  older_ciphers    = false
  cert_file        = "/config/auth/gui.pem"
}
