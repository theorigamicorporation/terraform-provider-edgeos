## ADDED Requirements

### Requirement: Generic config resource
`edgeos_config` SHALL manage the node at `path` as a single value (`value`), a multi-valued leaf (`values`), a whole subtree (`tree`, JSON) or a valueless node (none of them). At most one of `value`, `values` and `tree` MAY be set.

#### Scenario: Subtree ownership
- **WHEN** a child is added outside Terraform below a node managed with `tree`
- **THEN** the next plan shows the drift and apply removes the child

#### Scenario: Existing node
- **WHEN** the node already exists at create time
- **THEN** create fails and suggests importing it

#### Scenario: Import
- **WHEN** a node is imported by its CLI path such as `system time-zone`
- **THEN** the resource infers the mode from the node kind and import verification matches

#### Scenario: Non-string JSON values
- **WHEN** `tree` contains a number or boolean
- **THEN** plan fails asking for strings

### Requirement: Config data source
`edgeos_config` (data source) SHALL return `exists`, `kind` (`value`, `values`, `flag` or `tree`) and the matching value attribute for a path, and SHALL NOT fail when the node is missing.

#### Scenario: Listing tag nodes
- **WHEN** the path is `firewall name`
- **THEN** `children` lists the rule set names in sorted order

### Requirement: System info data source
`edgeos_system_info` SHALL report the firmware version string, release, build, platform code, the models sharing that platform, host name and domain name.

#### Scenario: Version parsing
- **WHEN** the router reports `EdgeRouter.ER-e50.v3.0.1.<build>`
- **THEN** `platform` is `e50`, `version` is `v3.0.1` and `models` lists the EdgeRouter X family
