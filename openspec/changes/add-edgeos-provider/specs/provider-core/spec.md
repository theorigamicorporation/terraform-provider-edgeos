## ADDED Requirements

### Requirement: Provider configuration
The provider SHALL accept `host`, `port`, `username`, `password`, `insecure`, `ca_certificate`, `timeout`, `transport` and `save`, each of which MAY instead come from an `EDGEOS_*` environment variable. Attributes set in the configuration SHALL win over environment variables.

#### Scenario: Configured only from the environment
- **WHEN** the provider block is empty and `EDGEOS_HOST`, `EDGEOS_USERNAME` and `EDGEOS_PASSWORD` are set
- **THEN** the provider connects with those values

#### Scenario: Missing credentials
- **WHEN** host, username or password is missing from both the configuration and the environment
- **THEN** configuration fails with an error naming the attribute and its environment variable

#### Scenario: Unsupported save mode
- **WHEN** `save = false` is set while only the api transport exists
- **THEN** configuration fails and explains that the web API always saves

### Requirement: Web API transport
The provider SHALL talk to EdgeOS through the web UI API: form login to `/`, reads through `/api/edge/partial.json` and writes through `/api/edge/batch.json` carrying the `X-CSRF-TOKEN` header.

#### Scenario: Session expiry
- **WHEN** the router answers a request with HTTP 403 or the login page
- **THEN** the provider logs in again once and repeats the request

#### Scenario: Wrong credentials
- **WHEN** the router serves the login page again after the login POST
- **THEN** the operation fails with "EdgeOS login failed"

#### Scenario: TLS
- **WHEN** neither `insecure` nor `ca_certificate` is set
- **THEN** the router certificate is verified against the system roots

### Requirement: Atomic and serialised writes
Each resource operation SHALL be sent as one batch that EdgeOS applies and commits as one transaction, and a provider instance SHALL never have two batches in flight.

#### Scenario: Commit failure
- **WHEN** EdgeOS rejects a batch at SET, DELETE, COMMIT or SAVE
- **THEN** the operation fails with the stage and every message EdgeOS returned, including the config path when known, and the router config is unchanged

#### Scenario: Parallel resources
- **WHEN** Terraform creates many resources in parallel
- **THEN** their commits reach the router one after another

### Requirement: Value validation
Values and node names SHALL be checked at plan time against the rules EdgeOS applies to every value: no double quote and no control characters other than tab.

#### Scenario: Double quote in a description
- **WHEN** a description contains `"`
- **THEN** plan fails before anything is sent to the router
