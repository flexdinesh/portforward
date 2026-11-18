# portforward

Manage SSH port forwards from one CLI on macOS and Linux.

<p align="center">
  <img src="docs/assets/cover.webp" alt="A terminal session: portforward add opens a host picker, list shows a disconnected forward, reconnect restores it, and list shows both forwards active." width="880">
</p>

Running `ssh -NfL 5432:localhost:5432 prod` works until you forget which
tunnels are open, a port is taken, or your laptop sleeps and the tunnel dies
without telling you. `portforward` keeps track of the forwards it creates, shows
their real status, and brings them back when they drop. It runs your installed
OpenSSH, so your `~/.ssh/config`, keys, agent, and jump hosts work unchanged.

## Quick start

```sh
brew install flexdinesh/tap/portforward
# or
go install github.com/flexdinesh/portforward/cmd/portforward@latest
```

```sh
portforward add prod 5432     # 127.0.0.1:5432 -> prod's localhost:5432
portforward list              # what is open, and whether it is still up
portforward remove 5432       # close it
```

## Features

- **Uses your SSH setup.** Host aliases, identities, ssh-agent, `ProxyJump`,
  and host-key checks come from OpenSSH and `~/.ssh/config`. Nothing to configure twice.
- **Interactive pickers.** Run `add` without arguments to pick a host from your
  forwards and SSH config (`Include` files included), filtered as you type.
  Run `remove` without arguments to pick forwards to close.
- **Stays up after you close the terminal.** Each SSH host gets one shared
  background connection; adding or removing a forward changes only that forward.
- **Honest status.** `list` reports `active`, `disconnected`, or `unknown`.
  It is read-only: it never prompts, opens new connections, or restarts tunnels.
- **One-command recovery.** After sleep or a network change, `reconnect`
  restores every disconnected forward with its saved mapping.
- **Scriptable.** `list --json` emits a stable array; exit codes are `0`
  success, `1` error, `2` invalid arguments, `130` cancelled.
- **Safe by default.** Binds to `127.0.0.1`, touches only SSH sessions it
  started, never kills processes or runs sudo, and stores no credentials.
  State is private and written atomically.

## Usage

```sh
portforward add                                     # pick host and port
portforward add prod                                # pick port
portforward add prod 5432                           # local 5432 -> prod's localhost:5432
portforward add prod 15432 --to localhost:5432      # different local port
portforward add bastion 15432 --to db.internal:5432 # a host reachable from bastion
portforward list [--json]
portforward reconnect
portforward remove                                  # pick forwards to remove
portforward remove 15432
portforward remove 5432 prod                        # only if it goes through prod
```

By default, local `127.0.0.1:<port>` forwards to `localhost:<port>` as seen from
the SSH host. `--to <host:port>` changes the destination and `--bind <address>`
changes the local address. Repeating an identical `add` succeeds without
duplicating; on a disconnected forward it reconnects.

`active` means the SSH connection is up and the forward is open. It does not
prove the service behind it is healthy.

## How it works

`portforward` starts a private OpenSSH master per SSH host, with its own control
socket so it never touches connections you started yourself. Forwards are added
and cancelled through that socket. Every change is recorded before SSH acts, so
an interrupted command can be retried safely. No daemon, no Go SSH stack.

## Learn more

[CLI reference](docs/cli.md) · [Architecture](docs/architecture.md) ·
[Development](docs/development.md) · [Releases](docs/release.md)
