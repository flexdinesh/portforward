# Architecture

One CLI mode, one Go module, no application daemon. Production dependencies are
the Go executable and an installed OpenSSH client. Build tools and the optional
integration-test SSH server are development dependencies.

## Ownership and dependencies

```text
cmd/portforward -> args -> forward
               -> forward
               -> state -> forward, privatefs
               -> ssh   -> forward, privatefs
               -> version
```

`cmd/portforward` resolves paths, constructs collaborators, handles signals, and
renders results. Configuration errors and invalid CLI arguments fail before
SSH effects. Help/version require neither state nor SSH.

`forward` owns listener identity, mapping validation, idempotency, conflicts,
connection generations, compensation, and recovery. Its two consumer contracts
are `Store` and `Tunnels`; it imports no CLI, persistence, or SSH adapter.

`state` owns schema validation and durable snapshot replacement. `WithLock`
owns and releases an exclusive OS lock; read snapshots use a shared lock. A
transaction is valid only inside its callback. Callbacks may save multiple
milestones while holding the lock. Separate processes cannot race the same
listener's lifecycle. Lock admission times out after 30 seconds; authentication
already holding the lock may take longer and is cancellable.

`ssh` owns private OpenSSH masters and their control sockets. Startup preserves
authentication and host-resolution configuration but suppresses unrelated
forwarding and command execution. Control operations ignore SSH config and
never connect to a new server. Three-second deadlines bound checks, additions,
cancellations, and shutdown requests. Only startup can authenticate. No PIDs
are persisted or signalled in production; shutdown uses the owned control socket.

`privatefs` owns the shared rule for control-data paths: current-user ownership,
0700 directories, 0600 regular files, and rejection of final-component symlinks.
It has no dependency on lifecycle or storage. `args` parses syntax and calls
the same mapping validation used by persistence and lifecycle operations.

## State and milestones

The first deployed schema is version 1. Each record includes a complete mapping,
a random connection ID, and phase `adding`, `active`, or `removing`. Listener
identity is canonical bind IP plus local port. Unknown schemas/fields, invalid
records, duplicate listeners, or connection IDs shared by different hosts fail
without replacing user state. Files are bounded to 8 MiB.

Add holds the exclusive lock throughout:

1. Read and validate state; reject conflicts and pending removals.
2. Return success for an identical active mapping with a live master.
3. Reuse a live owned generation for the host, or allocate a new connection ID.
4. Persist `adding` before starting SSH or requesting the forward.
5. Authenticate/start the master if needed; request the exact listener.
6. Persist `active` only after SSH accepts the forward.

A dead generation stays dead in older records. Reconnecting one mapping cannot
make an unrestored sibling look active; later explicit adds can join the new
generation. Different supplied aliases remain distinct SSH destinations.

Reconnect holds the exclusive lock while inspecting saved generations and
restoring disconnected mappings in list order through the same add milestones.
It leaves live mappings and pending removals untouched, reports unknown statuses,
and continues after individual failures unless cancelled. Each add reloads the
latest transaction snapshot, so later siblings can join a restored generation
without losing earlier state updates. Concurrent removal cannot be undone by
reconnection from a stale read.

For failures before the final snapshot is published, cancel the requested
forward, close an otherwise unreferenced master, and restore the original
snapshot. Compensation gets a separate five-second budget even after caller
cancellation. Failed compensation retains pending intent. Failed startup keeps
intent because detached authentication may have raced caller cancellation.

OpenSSH accepts an identical repeated forward request; cancellation of an
already absent mapping is treated as success. These adapter guarantees make
pending adds/removals retryable without guessing from saved PIDs or local TCP
connections. The latter could belong to unrelated software or require
destination-application availability.

Remove validates the optional host guard, persists `removing`, cancels exactly
one mapping, and closes the master only when no other record references that
generation. It deletes the record last. A failure after cancellation leaves an
intent record for the next removal to finish. Adds cannot override that intent.
Absent mappings and dead masters require no external termination.

## Publication and durability

Save writes a private temporary file, syncs it, closes it, atomically renames it,
then syncs its containing directory. Errors before rename preserve the previous
snapshot. Errors after rename return `PublishedError`: the replacement is visible
but its survival across power loss is uncertain. An accepted add retains its
listener when its active snapshot was already published, avoiding a cancelled
listener paired with visible active state. Retry safely observes that mapping.

These are recoverable milestones across two systems, not a transaction spanning
SSH and the filesystem. Initial intent is durable before external effects. A
hard interruption leaves that intent visible for retry or removal. Orphaned
temporary state files have no authority and are never loaded.

## Observation and shutdown

List reads a consistent state snapshot, then checks each distinct recorded
generation once. It never authenticates or restores tunnels. A live master plus
an active record yields `active`; a dead master yields `disconnected`; a control
failure or live pending operation yields `unknown` plus a diagnostic. Results
are sorted by canonical bind address and numeric port. The remote application
can still be unavailable while the tunnel is active.

Ctrl-C/SIGTERM cancels foreground work. Deferred locks and subprocess waits
finish before the CLI exits. Successfully accepted masters intentionally
outlive the CLI; removing their last mapping requests shutdown. No automatic
restart after sleep/reboot is promised.

## Verification

Focused tests cover parsing, shared connections, conflicts, state-write failures,
compensation, interrupted retries, generation isolation, and inspection failures.
A transitive import guard keeps lifecycle behavior independent of app adapters.
Real-store tests cover reopening, private permissions, corrupt-state preservation,
cross-handle locking, concurrent updates, and cancelled lock admission.

The tagged integration suite builds the real executable and forwards actual TCP
traffic through an isolated OpenSSH server. It exercises configured-forward
suppression, duplicate/pending requests, occupied ports, host guards, stale
sockets, reconnects, pending removals, IPv6 listeners, remote application
unavailability, and unrelated-session isolation.

Linux runtime behavior is tested locally. macOS binaries are cross-compiled;
macOS runtime behavior still requires execution on macOS. Interactive password
and hardware-key prompts depend on the user's terminal and OpenSSH setup; the
automated SSH fixture authenticates with a temporary key.
