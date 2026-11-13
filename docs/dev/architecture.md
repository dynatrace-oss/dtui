# dtui architecture

How the pieces fit. Start at [AGENTS.md](../../AGENTS.md) for the rules and
the task routing; the full design is [tui.md](../design/tui.md). When this
file and the code disagree, the code wins: fix this file in the same PR.

## Relationship to dtctl

dtui is its own module (`github.com/dynatrace-oss/dtui`) and a pure
**consumer** of the published `dtctl` modules (ADR-0004, ADR-0005):

- It reads dtctl's config (`~/.config/dtctl/config`) and keyring **without ever
  writing them**. Contexts are created/managed with `dtctl ctx set`. A
  `--context` flag or `DTCTL_CONTEXT` env override is applied in memory only
  (session-local) — an open TUI must never repoint other dtctl processes.
- It imports dtctl's `sdk/session` (config, credentials, client), `pkg/exec`
  (DQL executor), `pkg/resources/*` (SLO/detector/analyzer/segment handlers),
  and `pkg/output` renderers. **No business logic is forked** — the TUI is a
  presentation layer.
- The charmbracelet/bubbletea dependencies live in this module only; dtctl's
  root module and SDK stay TUI-free. The dtctl module versions are pinned
  to released tags in `go.mod` (plain `require`, no `replace`).

`main.go` and `sources.go` are the wiring seam: they construct the tenant
client and adapt dtctl resource handlers into `tui.Source` closures, so that
**`internal/tui` never does HTTP itself**. Follow this — new API-backed views
get a source constructor in `sources.go`, tested with an interface stub.

## Architecture

The single most important design decision: **DQL is the substrate, not the
interface** (ADR-0001). Every view is a curated DQL query under the hood; every
drill-down key composes scope (entity + timeframe) into the next query
automatically, so the user never types DQL. `ctrl+q` reveals the generated
query when curation runs out.

### The declarative view catalog (`internal/tui/catalog/`)

This is where you add or change views, and it's the part that most needs
reading before editing. Views are **data, not bespoke screens** (ADR-0003): a
`Spec` bundles a scope-aware DQL template, curated columns, metric-column
enrichment, drill-down targets, relations, and a tabbed detail spec. Adding a
cloud resource type or signal view = adding a `Spec`, not writing a screen.

The package is deliberately **free of any TUI-framework dependency** so query
composition tests as plain data. `catalog.go`'s package doc lists the
per-entity-type dispatch points (KeyFacts, MetricsFor, smartscapeField,
SpanScopable, VulnEntityFilter, …) to walk when curating a new Smartscape type
— they are separate switches on purpose because each encodes independently
live-validated semantics. Files are organized one-per-domain (`k8s`, `spans`,
`security`, `metrics`, `genai`, `cloud`, `synthetic`, …).

`Scope` (entity + `Timeframe`) is threaded through every query template.
`Timeframe.DQL()` handles both relative (`now() - 2h`) and absolute
(`toTimestamp(...)`) windows through the same `from:` slot.

### The TUI shell (`internal/tui/`)

Elm-style bubbletea: `app.go` is the root `tea.Model` holding the view stack,
command bar, routing, and global keys. `table.go` / `detail.go` are generic
engines driven by a `Spec`; `problem.go`, `vulnerability.go`, `waterfall.go`,
`timeline.go`, `navigator.go` are bespoke pages. `datasource.go` turns
`pkg/exec` and resource calls into `tea.Cmd`/`tea.Msg` with `context.Context`
cancellation tied to view lifetime. `theme/` is a lipgloss adapter over
dtctl's `ColorEnabled()`.

**Layering rule:** nothing imports `internal/tui` except the `dtui` main
package; no HTTP inside `internal/tui`; every API call is a `tea.Cmd` goroutine.

**Rendering rule:** record data is untrusted terminal input. Text that reaches
the screen from a record goes through `catalog.SafeText` before any theme
styling (it turns control characters into visible glyphs like `␛`).
`Column.Text`/`TextAt`, `PreviewTitle`/`PreviewFacts`/`PreviewValue` and the
inspector's `renderString` already do this. Only add it when you render a
record field some other way, e.g. building a label from `catalog.Str`. Never
put it in `flatten`/`pad`/`wrap`, because those also get already-styled text.
Never use it on values used for queries or yank, which must stay raw.
`app.View()` also runs every frame through `sanitizeFrame`, which lets only
color codes (SGR) through. That is a safety net, not a replacement for
`SafeText`.

### Runtime discovery

Shapes are hardcoded only as *defaults*; the rest is discovered per environment
and cached per session (refreshed on context switch): node-type census, edge
catalog, field census, metric catalog, and the semantic dictionary (ADR-0010).
This keeps the catalog small — curated defaults where curation adds value,
introspection everywhere else.

### Workspace file (`internal/workspace/`)

A committed `.dtui.yaml` (found by walking up like `.git`) supplies
session-local defaults: matching context by environment URL, initial view,
timeframe, and Grail filter segments. It carries **no credentials or executable
keys by construction** — it can only narrow what a session shows. A broken file
degrades to a startup warning, never a failed launch.
