resource "edgeos_port_forward_settings" "this" {
  wan_interface  = "eth0"
  lan_interfaces = ["switch0"]
  hairpin_nat    = true
  auto_firewall  = true
}
