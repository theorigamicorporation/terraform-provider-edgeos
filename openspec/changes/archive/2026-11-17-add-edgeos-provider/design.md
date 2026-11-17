# Design

## Context

EdgeOS is a Vyatta fork. The configuration is a tree of nodes: interior nodes,
tag nodes (named instances such as `firewall name WAN_IN` or `rule 10`), leaf
nodes with one value, leaf nodes with many values (`address`, `name-server`) and
valueless nodes (`disable`). There is no public API, but the web UI talks to a
small JSON API that is stable across EdgeOS 1.x, 2.x and 3.x.

## Transport evaluation

### Web UI API (chosen as primary)

- Login: `POST /` with form fields `username` and `password`. Success is a
  `303` redirect and two cookies, `PHPSESSID` (session) and `X-CSRF-TOKEN`.
  A failed login returns `200` with the login page again.
- Reads: `GET /api/edge/get.json` returns a fixed set of top level nodes
  (firewall, interfaces, service, system, vpn, protocols, ...). It does not
  include `port-forward`, `policy`, `zone-policy` or `load-balance`.
  `GET /api/edge/partial.json?struct=<json>` returns only the requested
  subtrees, where `null` in the struct means "everything below here", and it
  works for any top level node. A path that does not exist comes back as
  `null`, which is indistinguishable from a valueless node, so the client reads
  the parent of the node it cares about and checks for the key.
- Writes: `POST /api/edge/batch.json` with a JSON body
  `{"DELETE": {...}, "SET": {...}}` and the `X-CSRF-TOKEN` header equal to the
  cookie. The daemon applies DELETE then SET, commits and saves to
  `config.boot` in one go. The GUI itself relies on that order (it deletes and
  re-sets the same leaf in one batch). The response carries a result per stage:
  `{"SET": {"success": "1", "failure": "0"}, "COMMIT": {...}, "SAVE": {...}}`.
  Errors are either a map of `path -> message` or a string where entries are
  separated by U+FFFE and each entry looks like `[ path ]\nmessage` followed by
  U+FFFF and a status digit.
- Values in the JSON tree: a string is a single value, an array is a
  multi-valued leaf, `null` is a valueless node or a tag instance without
  children, and an empty top level node is `""`.
- An expired or missing session gets `403`. The client logs in again once and
  retries; a `403` means the request was not executed, so this is safe for
  writes too.
- Version and model: `GET /api/edge/data.json?data=sys_info` returns
  `sw_ver` such as `EdgeRouter.ER-e50.v3.0.1.5800000.250900.1200`.

Pros: structured JSON both ways, atomic commit with error details per path,
no PTY scraping, same behaviour as the GUI. Cons: undocumented; the batch call
always saves, so `save = false` cannot be honoured with this transport.

### SSH and the Vyatta CLI (documented for later)

`configure`, `set`, `delete`, `commit`, `save` through an SSH session. Reading
back typed values is harder: `show configuration commands` does not say which
leaves are multi-valued or which nodes are tags, so the transport would need
`cli-shell-api` calls or the templates under
`/opt/vyatta/share/vyatta-cfg/templates`. A known problem on EdgeOS 3.0.1 is
that commits run through `/opt/vyatta/etc/functions/script-template` fail for
firewall rule sets with `iptables-restore: line 2 failed`, while the same
commands in an interactive PTY session work. An SSH transport must therefore
drive an interactive PTY (`ssh -tt` style, commands piped into vbash). It is
the only way to commit without saving.

### Decision

The web UI API is the primary and, for now, only transport. The provider talks
to it through `client.Transport`:

```go
type Transport interface {
    Login(ctx context.Context) error
    Read(ctx context.Context, path configtree.Path) (any, bool, error)
    Apply(ctx context.Context, b *configtree.Batch) error
    SystemInfo(ctx context.Context) (SystemInfo, error)
}
```

`transport` is still a provider attribute so that `ssh` can be added without a
breaking change. `save = false` is rejected with a clear diagnostic while only
the api transport exists.

## Data model

`internal/configtree` holds the tree types and is free of HTTP and Terraform
code, so it is the part with the densest unit tests:

- `Path` is a list of node names. `Path.String()` renders the CLI form with
  quoting (`firewall name "My rules" rule 10`) and `ParsePath` reads it back.
  Fuzz tests check that the two round-trip for any input.
- `Leaves` is a flat map from relative path to `Value` (single, multi or
  flag). Each resource converts its model to `Leaves` and back.
- `Diff(base, old, new Leaves)` returns a `Batch`: removed or changed leaves
  are deleted, new or changed leaves are set. A changed multi-valued leaf is
  deleted and set again in the same batch, which replaces the whole list.

Resources own a fixed set of leaves below a base path. Unknown children (for
example rules below a firewall name managed by `edgeos_firewall_rule`) are
never touched by an update. Delete removes the base path for tag instances and
only the owned leaves for singletons such as `edgeos_system` or a physical
ethernet interface.

## Concurrency

`client.Client` wraps the transport and holds a mutex around `Apply`, so one
provider instance never has two commits in flight. Terraform runs resources in
parallel, and EdgeOS config sessions fail on concurrent commits.

## Fake server

`internal/fakeedgeos` implements login, `get.json`, `partial.json`,
`batch.json`, `data.json` and `heartbeat.json` over an in-memory tree with the
same value encoding as the real device. It has set-time validators (enum and
range checks), commit-time validators (references such as an interface
pointing at a missing firewall name) and test hooks for failing the next commit,
expiring sessions and editing the tree out of band. `cmd/fake-edgeos` runs it
locally so people can try the examples.

## Testing

- Table driven unit tests for path rendering, leaves, diffs, batch JSON and the
  model mapping of every resource.
- Acceptance tests (`TF_ACC=1`) against the fake server for every resource:
  create, update in place, replacement, import, drift, delete and error paths.
  Run with both `terraform` and `tofu` through `TF_ACC_TERRAFORM_PATH`.
- Real-device tests behind `EDGEOS_ACC_REAL=1`. They write to the router and
  never run in CI.
- Coverage gate of 80 percent over `internal/...` in CI.

## Risks

- The API is undocumented and could change between firmware releases. The
  client is lenient when parsing results (strings or numbers for status
  fields, map or string errors).
- Deleting a whole firewall name also deletes rules not managed by Terraform.
  This is documented on the resource.
