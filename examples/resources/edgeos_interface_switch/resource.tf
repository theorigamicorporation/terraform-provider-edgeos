resource "edgeos_firewall_name" "lan_in" {
  name = "LAN_IN"
}

# The built-in switch of an EdgeRouter X, made VLAN aware, with eth1 left out
# for use as a separate port.
resource "edgeos_interface_switch" "lan" {
  name         = "switch0"
  description  = "Local"
  addresses    = ["192.168.1.1/24"]
  switch_ports = ["eth2", "eth3", "eth4"]
  vlan_aware   = true
  firewall_in  = edgeos_firewall_name.lan_in.name
}
