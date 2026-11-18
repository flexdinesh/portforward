# Rehearsal

- Started from: `readme-cover` and `hero-demo` (terminal chrome, corner bleed).
- Changed: one `chrome="terminal"` window, 48px gutter top and left, off the
  right and bottom; no satellite, no cursor. Real strings only, matching
  `cmd/portforward` and `internal/picker` output.
- Story: `add` host picker filters to `prod`, port prompt, `Added`; `list`
  shows bastion disconnected; `reconnect`; `list` shows both active (poster);
  `clear` resets the loop.
- Gate: `film.mjs --gate` fails `empty pane` for the first ~3s after `clear`
  (terminal mostly empty). Accepted by the maintainer; a real cleared terminal
  starts empty.
- Not checked: a `compact` (phone-width) design; GitHub scales the image.
