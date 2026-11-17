data "edgeos_system_info" "router" {}

output "firmware" {
  value = "${data.edgeos_system_info.router.version} on ${data.edgeos_system_info.router.models}"
}
