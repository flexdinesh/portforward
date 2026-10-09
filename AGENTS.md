# Instructions

Read `~/.codex/machine-instructions.md`. Keep interactions and commits concise.
Make minimal changes. Use `wt` for worktrees; fetch before creating one.

## Repository

Go CLI managing local SSH forwards on macOS/Linux. Contracts in `docs/cli.md`;
ownership and recovery in `docs/architecture.md`.

- `cmd/portforward`: configuration, composition, signals, terminal/JSON output.
- `internal/args`: CLI parsing; no side effects.
- `internal/forward`: mapping validation, lifecycle rules, consumer contracts.
- `internal/state`: private versioned snapshots, cross-process locks, atomic saves.
- `internal/ssh`: owned OpenSSH masters and bounded control operations.
- `internal/privatefs`: shared private-file ownership/permission rules.
- `internal/version`: linker and Go module version reporting.
- `tools/release-version`: stable tag selection.
- `tools/homebrew-formula`: formula generation from published checksums.

Keep commands in `mise.toml`; hooks and workflows share these tasks.
Run `mise run check` before calling work done. Use targeted tests while iterating.
Use standard library first, idiomatic Go, explicit errors, deterministic output,
and context-aware blocking operations. Test semantic behavior.
Keep SSH execution, state persistence, and rendering separate.
Run `mise run test:integration` after SSH/lifecycle changes; requires local sshd.
Persist intent before SSH effects. Preserve connection IDs and retry semantics.
A published state error differs from a failure before replacement.
Do not claim a live SSH master proves the remote application is reachable.
Do not interfere with SSH sessions not owned by portforward.

At the end of plans, list unresolved questions, if any.
