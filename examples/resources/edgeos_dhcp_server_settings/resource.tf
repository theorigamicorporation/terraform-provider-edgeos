resource "edgeos_dhcp_server_settings" "this" {
  disabled        = false
  hostfile_update = true
  static_arp      = false
  use_dnsmasq     = false
  global_parameters = [
    "option space ubnt;",
    "option ubnt.unifi-address code 1 = ip-address;",
  ]
}
