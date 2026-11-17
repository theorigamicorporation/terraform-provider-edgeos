# Security policy

## Supported versions

Security fixes go into the latest minor release.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability reporting
("Report a vulnerability" under the Security tab of this repository) or write
to office@theorigamicorporation.com. You should get an answer within a week.

Include the provider version, the Terraform or OpenTofu version, the EdgeOS
version and steps to reproduce. Leave out real passwords, keys and addresses.

## Things to know

- The provider sends the router password over HTTPS to the EdgeOS web UI. Use
  `ca_certificate` instead of `insecure = true` where you can, so the
  connection cannot be intercepted.
- Secrets such as WireGuard private keys end up in the Terraform state when
  managed inline. Prefer key files under `/config/auth` on the router, and
  protect your state.
- Debug logs (`TF_LOG=DEBUG`) contain the config changes sent to the router,
  including values, but never the login password.
