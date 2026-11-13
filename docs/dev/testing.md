# Testing conventions

No HTTP and no TTY anywhere in tests (see `docs/design/tui.md` "Testing").

- **Model tests** (`app_test.go` and friends): drive the real app with
  synthetic `tea.Msg` key events and assert navigation, scope composition, and
  read-only behavior. Timers run at zero delay — no test ever sleeps.
- **Catalog tests**: render each `Spec`'s query against fixture scopes and pin
  the DQL shape with substring checks (catches scope-composition regressions).
- **View assertions**: check `View(w,h)` output via `ansi.Strip` + substring
  matching for content.
- **Layout snapshots** (harness in `internal/tui/snapshot_test.go`, one
  `<view>_snapshot_test.go` per view): the standard for pinning how a view
  looks. `assertSnapshots` diffs the stripped screen
  against `testdata/<view>-<w>x<h>.golden` at the canonical sizes 80×24,
  100×40, 120×40, 160×50 (one per preview-layout branch). Regenerate with
  `go test ./internal/tui -run Snapshot -update`; seed rows shaped like
  real Grail records and call `pinSnapshotClock(t)` for time cells. Keep coverage narrow: **main
  screens and their sanity states only** — not every overlay, picker, or
  edge state — so small feature changes don't churn the suite.
- **Datasource tests**: replace the executor via the `runFn` seam; API-backed
  views stub the injected `Source` closures; rows via `seedRows`.
- Use **synthetic identifiers only** — never real tenant/entity IDs, per the
  privacy rule in `docs/dev/learnings.md`.
