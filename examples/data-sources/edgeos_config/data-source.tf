data "edgeos_config" "host_name" {
  path = ["system", "host-name"]
}

# List the firewall rule sets that exist on the router.
data "edgeos_config" "rulesets" {
  path = ["firewall", "name"]
}

output "rulesets" {
  value = data.edgeos_config.rulesets.children
}

# Decode a subtree.
data "edgeos_config" "eth0" {
  path = ["interfaces", "ethernet", "eth0"]
}

output "eth0_description" {
  value = try(jsondecode(data.edgeos_config.eth0.tree).description, null)
}
