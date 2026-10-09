# portforward

Manage local SSH port forwards on macOS/Linux. Requires `ssh`; uses `~/.ssh/config`.

## Install

```sh
# Stable (after the first release).
brew install flexdinesh/tap/portforward
# Or with Go.
go install github.com/flexdinesh/portforward/cmd/portforward@latest

# From this checkout.
mise trust
mise run install
```

## Usage

```sh
portforward add 5432 prod
portforward add 15432 prod --to localhost:5432
portforward add 15432 bastion --to db.internal:5432
portforward list
portforward list --json
portforward remove 15432
portforward remove 5432 prod
portforward --help
```

Default: `127.0.0.1:<port>` → remote `localhost:<port>`. Use `--to` to change the
destination; `--bind` to change the local address. Tunnels survive terminal closure.
`list` shows managed tunnels only. Repeat `add` to reconnect a disconnected tunnel.

## Releases

Run the GitHub **Release** workflow. Patch increments automatically; change
`.release-version` (`0.1`) to choose major/minor. Releases publish Go binaries and
open a Homebrew tap PR. Requires the `HOMEBREW_TAP_TOKEN` repository secret.

[CLI reference](docs/cli.md) · [Development](docs/development.md) · [Releases](docs/release.md)
