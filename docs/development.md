# Development

Project commands live in `mise.toml`, following gitsy's tooling pattern.
Go tracks the `1` series; Lefthook and GoReleaser use exact versions.
The module's minimum Go version is 1.26. No third-party Go dependencies.

```sh
mise trust
mise run setup
mise run check
mise run run -- --help
mise run run -- --version
```

| Task | Purpose |
| --- | --- |
| `mise run fmt` | Format Go |
| `mise run tidy` | Tidy module |
| `mise run test` | Unit and persistence tests |
| `mise run test:integration` | Built CLI against local OpenSSH server |
| `mise run test:package ./cmd/portforward` | Selected package tests |
| `mise run check` | Formatting, tidiness, vet, race tests, build |
| `mise run ci` | Formatting and build |
| `mise run build` | Build `bin/portforward` |
| `mise run install` | Install local CLI into Go bin directory |
| `mise run hooks` | Install Lefthook pre-push hook |
| `mise run release:config` | Validate GoReleaser config |
| `mise run release:version` | Select next stable tag |

The pre-push hook validates each distinct pushed commit in a temporary archive,
including annotated tags. Working files cannot mask a failing committed revision.
`PORTFORWARD_CHECK_ROOT` selects that snapshot; temporary files are removed after
checks. Race tests require a C compiler. CI remains light; manual releases run
full checks. No demo task or TUI dependencies are needed.

## SSH integration tests

`mise run test:integration` requires `ssh`, `sshd`, and `ssh-keygen` on PATH.
It builds the production binary and starts an isolated loopback SSH server
using temporary keys, a temporary config, strict host-key verification, and
per-test state. It does not modify your SSH config or contact external hosts.
Missing prerequisites fail this explicit task; ordinary checks need no server.

Coverage includes actual TCP traffic, shared masters, duplicate and pending adds,
busy ports, host guards, stale sockets after a killed master, reconnection,
interrupted removals, and unrelated session isolation. Fixtures join their
server and application workers and clean up owned SSH masters.

See [architecture](architecture.md) and the [CLI reference](cli.md).
