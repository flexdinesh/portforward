# CLI reference

Local TCP forwards, plain terminal output, and existing OpenSSH authentication
and configuration. Requires an installed OpenSSH client on macOS or Linux.

## Commands

```sh
portforward list [--json]
portforward reconnect
portforward add <port> <host> [--to <host:port>] [--bind <address>]
portforward remove <port> [<host>] [--bind <address>]
portforward --help
portforward --version
```

`<port>` is the local port, an integer from 1 to 65535. `<host>` is the SSH
destination: an alias from `~/.ssh/config`, a hostname, or `user@hostname`.
Default bind address: `127.0.0.1`. Default destination: `localhost:<port>` as
seen from the SSH server. Ports requiring privileges report the OS error;
the tool never invokes sudo.

Flags may follow positional arguments. IPv6 destinations use `[::1]:5432`.
`--bind` accepts an IP address, including `::1`; non-loopback exposure requires
an explicit address. SSH port, identity, and jump-host settings belong in SSH
config initially, avoiding a second SSH configuration surface.

| Example | Result |
| --- | --- |
| `add 5432 prod` | Local 5432 → prod's localhost:5432 |
| `add 15432 prod --to localhost:5432` | Local 15432 → prod's localhost:5432 |
| `add 15432 bastion --to db.internal:5432` | Local 15432 → database reachable from bastion |
| `remove 15432` | Remove default-bind local 15432 |
| `remove 5432 prod` | Remove only if its recorded SSH destination is prod |
| `remove 5432 --bind ::1` | Remove the IPv6-loopback listener |

## List

List only forwards created by portforward for the current OS user. Read-only;
do not connect to new hosts, prompt for authentication, or restart tunnels.

```text
LOCAL            SSH HOST  DESTINATION       STATUS
127.0.0.1:5432   prod      localhost:5432    active
127.0.0.1:15432  bastion   db.internal:5432  disconnected
```

Sort by bind address then numeric local port. `active` means the owned SSH
master responds to a bounded control check and the forward is recorded as
established; it does not prove the destination application is healthy. A dead
master leaves its entries visible as `disconnected`. A timeout or inconsistent
state reports `unknown` with a diagnostic rather than guessing.

`--json` emits a stable array, including `[]` for no entries; diagnostics go to
stderr. Fields: `bind_address`, `local_port`, `ssh_host`,
`remote_host`, `remote_port`, `status`. Plain output says `No managed forwards.`
when empty. Known disconnections are successful list results; state corruption
or inability to inspect state is an error.

## Reconnect

`portforward reconnect` retries all disconnected forwards using their saved SSH
host, bind address, and destination. No arguments or flags are needed. Active
forwards stay untouched. Pending removals are skipped; retry `remove` to finish
them. Unknown statuses are reported as errors without attempting reconnection;
live pending additions still require an explicit `add` or `remove`.

Forwards are attempted in list order. Each successful reconnection prints a
confirmation. A failure is reported without preventing attempts for remaining
forwards; any failure returns exit code `1`. Cancellation stops further attempts.
When nothing needs reconnecting, print `No disconnected forwards to reconnect.`
and exit successfully. Reconnection may prompt for SSH authentication. It is an
explicit action; `list` remains read-only and no automatic restart is promised.

## Add and remove

`add` authenticates if needed, creates the listener, then returns. The tunnel
survives closing the invoking terminal. Success means SSH accepted the local
listener, not that the database or other destination service is reachable.

The managed identity is `(bind address, local port)`, not `(port, SSH host)`.
A repeated identical active mapping succeeds without duplication. An identical
disconnected mapping reconnects on explicit `add` or `reconnect`. A different
mapping occupying the same managed listener fails and shows the existing
destination. Overlapping wildcard binds can also conflict; SSH's actual bind
result is authoritative.
Never kill another process to free a busy port.

`remove` cancels exactly the selected forward. An optional host is a guard
against removing a mapping on the wrong host, not another part of its identity.
A missing entry succeeds with `No matching forward.` A host mismatch fails.
Other forwards through the same host remain active; removing the last forward
closes its owned master. Dead-master records can be removed safely without
signalling stale PIDs.

Exit codes: `0` success, `1` operational error, `2` invalid arguments. Errors go
to stderr. `add` and `remove` produce one short confirmation line. During
authentication, OpenSSH may display its normal prompts and diagnostics.

## Implementation approach

Use the installed OpenSSH client via Go's `os/exec`, with explicit argument
arrays. This preserves config aliases, ssh-agent, host-key checking, and
ProxyJump. OpenSSH provides master control operations for checking, requesting,
and cancelling forwards. See [the OpenSSH manual](https://man.openbsd.org/ssh).

Share a private live master per supplied SSH destination. Each connection
generation has a random ID; reconnecting one forward does not change the
status of disconnected siblings from an older generation until each is restored.
Aliases pointing at the same server remain separate destinations. Private control sockets isolate
the tool from the user's configured ControlPath and independently started
sessions. Master startup clears configured LocalForward/RemoteForward and
disables agent/X11 forwarding, local commands, remote commands, and TUN devices.
Control requests ignore SSH config so only the requested mapping is changed.

An authenticated master runs in the background; `-O forward` and `-O cancel`
manage its listeners. Connection attempts and control operations have deadlines;
keepalives detect broken connections. Control sockets identify owned masters.
No application daemon or Go SSH implementation is required.

Owned mappings use schema version 1, a private directory, atomic writes, and
an OS lock around mutations. State defaults to `$HOME/.local/state/portforward`
on Linux and `$HOME/Library/Application Support/portforward` on macOS. An
absolute `$XDG_STATE_HOME` overrides the parent directory on either platform.
Control sockets live in a private directory under `/tmp`, namespaced by the
current user and state root to keep paths short. No credentials are stored.

SSH and state writes are not a transaction. Adds and removals persist intent
before changing SSH. A failed add after listener creation rolls back that
listener; an incomplete rollback retains its pending record. A failed or
interrupted startup also retains intent because authentication could have
completed while cancellation was delivered. Retry the identical `add`, or use
`remove` to discard it. A pending removal must finish via `remove` before a new
`add` can use that listener.

After a cancellation, a failed state write leaves a retryable removal record.
Pending operations with a live master show `unknown`; records with a dead master
show `disconnected`. Corrupt or unsupported state is rejected and preserved.
State lock waits are bounded to 30 seconds; control requests to 3 seconds.
Initial network connection attempts have a 15-second timeout. Interactive
OpenSSH authentication remains interactive and can be cancelled with Ctrl-C.

If a replacement was published but directory durability could not be confirmed,
the command reports the error and retains the SSH effect matching the visible
snapshot. Retrying is safe. See [architecture](architecture.md) for ownership
and recovery details.

## Challenges and later ideas

- A port alone does not specify the remote port or service host. `--to` covers
  both without making the common case verbose.
- Requiring a host for removal adds typing. Bind address plus port already
  selects the listener; retain the host as an optional guard.
- Listing every SSH forward on the machine is unreliable: process/socket
  inspection cannot consistently recover remote destinations, and multiplexed
  forwards may not appear in command lines. Promise managed forwards only.
- A background tunnel can die after Wi-Fi changes or sleep. Show that honestly;
  explicit `add` or `reconnect` restores tunnels. Automatic reconnection requires
  supervision and should be a later, deliberate feature.
- Useful next additions: `list --watch`, named presets, and `doctor` for SSH,
  socket, and state diagnostics. Add bulk removal only with a clear selector.
- Defer SOCKS, reverse forwarding, startup restoration, and a full-screen TUI.
  They expand lifecycle and configuration complexity beyond this first use case.
