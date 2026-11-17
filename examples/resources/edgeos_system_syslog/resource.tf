# The EdgeOS defaults, plus a larger archive.
resource "edgeos_system_syslog" "this" {
  facilities = {
    all       = "notice"
    protocols = "debug"
  }
  archive_files = 5
  archive_size  = 1024
}
