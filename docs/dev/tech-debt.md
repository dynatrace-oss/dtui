# Tech debt

Known shortcuts, gaps and duplication, so they are fixed on purpose rather
than rediscovered. Add an entry when you leave one behind; delete it in the
PR that fixes it. One entry: what, where, why it matters, the fix.

## Near-duplicate code below the lint threshold

CI runs `dupl` at 150 tokens. At 100 tokens it still finds two pairs:
`internal/tui/catalog/specs.go` (two eight-line column blocks) and
`internal/tui/history.go` (two fifteen-line blocks). Small, but agents copy
what they find. Fix: extract the shared part, then lower the threshold.

## Live Debugger snapshot fields are unvalidated

`docs/dev/learnings.md` marks the `application.snapshots` facts as taken
from dtctl's implementation and not yet validated live by dtui; the host,
process and trace association fields are read tolerantly in
`internal/tui/catalog/livedebugger.go`. Fix: validate on a tenant with
Live Debugger data and update the learnings entry.

## Preset save does not store the table lens

`W` writes view + facets + searches. A table opened on a non-default lens
(`[` / `]`) replays on the spec's default lens. Storing `scope.Lens` would
need a new YAML field and a restore path in `applyNamedPreset`. Fix: add
`lens` to the preset schema if a real preset needs it.

## learnings.md is one long file

`docs/dev/learnings.md` is over 1,700 lines, so an agent looking for one fact
loads all of it or greps. Fix: keep the section numbering stable and, if it
keeps growing, split section 1 (DQL facts) by data object.

## DQL editor miscounts ambiguous-width characters in CJK locales

The query editor (`internal/tui/query.go`) is the `textarea` of bubbles
v1.0.0, which measures a line with `uniseg` in some places and with
`go-runewidth` in others. The two disagree on East Asian ambiguous-width
characters (`Ω`, `é`) once go-runewidth counts them as wide. Under
`RUNEWIDTH_EASTASIAN=1` a wrapping line then hides the character at the
boundary (two since go-runewidth 0.0.31), although the submitted query
holds it. Under that variable or `LC_ALL=ja_JP.UTF-8`, moving the cursor up
onto a line of such characters places the next edit one cell to the left;
go-runewidth 0.0.31, which dtctl requires since 0.46.0, extends that to a
CJK locale named without a charset (`LC_ALL=ja_JP`). Bubbles' `textinput`
(truncation) and `glamour` (wrapping) measure with go-runewidth too and
shift with it. Fix: one width
convention in bubbles' `textarea` upstream, or a wrapper in dtui that
measures both paths the same way.

## No workflow builds the browser target

`wasm/build.sh`, the patches it applies and `cmd/web` are compiled only when
someone runs the script; `.github/workflows/ci.yml` builds natively. A dtctl
bump that moves a patched file, or a `Source` whose signature
`cmd/web/sources.go` must follow, breaks the extension unnoticed, and the
script's refusals (a module with no extracted source, a source inside
`wasm/.deps`) are checked by hand. Fix: a CI job that runs the script
without `wasm-opt`.

## A context switch races with commands already built

`dataSource.call` and `dataSource.run` in `internal/tui/datasource.go` read
the sources, the executor and the segment options inside the command they
return, on the command's goroutine. A context switch assigns all three from
`Update` in `internal/tui/app.go`. A command built before the switch and run
after it queries the new context, and the read is a data race with the
write. Fix: capture the executor, the sources and the options when the
command is built.

## SLO evaluations cannot be cancelled before they start

`mergeSLOEvaluation` in `sources.go` calls `Evaluate`, which takes no
context, and the wait for a semaphore slot in front of it does not watch the
context either. The evaluations a call has queued therefore all start, and
run to their end, after its context is done. Fix: give up the wait and skip
the call once the context is done.

## Log pattern analysis ignores an absolute timeframe

The analyzer input in `sources.go` and `cmd/web/sources.go` sets its window
to `"now-" + scope.Timeframe.Label`. For an absolute window (`Absolute()`
on the timeframe) that is not the window the view shows, so the analyzer
and the `logQuery` beside it clip each other. Fix: send the timeframe's
`From` and `To` when it is absolute.

## Guards in invariants_test.go with known gaps

`TestOnlyEntryPointsImportTUI` exempts directories by the prefix
`internal/tui` without a trailing slash, so a sibling `internal/tuifoo`
could import the shell. `TestTUIOpensNoListener` matches the identifier
`net`, so an aliased import passes. `TestTUIHasNoDirectTenantAccess` and
`TestWorkspaceHasNoTenantAccess` refuse `net/http` but no other HTTP
client. `TestCatalogIsFrameworkFree` knows the terminal framework by the
prefix `github.com/charmbracelet/` only, and would match nothing after a
move to a release published under `charm.land/`. An import walk over a
folder that holds no non-test Go file passes every guard built on it. And
nothing refuses a `replace` or `exclude` in the committed
`go.mod`, with either of which `go install github.com/dynatrace-oss/dtui@latest`
rejects the module. Fix: compare whole path elements, resolve the import
behind the identifier, name the HTTP clients in the module graph, match the
framework under both hosts, fail a walk that reads no file, and fail on a
`replace` or `exclude` directive.

## Partial-result classifier is a copy of dtctl's

`classifyNotice` in `internal/tui/notice.go` mirrors dtctl's mapping of
query notifications and has fallen behind it: `FETCH_EXEC_TIME_LIMIT` is
classified only when its message happens to mention a timeout. The comment
on `noticeFor` says dtctl keeps its classifier unexported, which is no
longer true. Fix: call dtctl's exported `exec.PartialCause` and drop the
copy.

## make build stamps an empty version outside a git checkout

In `makefile`, the `dev` fallback of `VERSION` is attached to `sed`, which
succeeds on empty input, not to `git describe`. In a source archive
`make build` therefore stamps an empty string, which `dtui --version`, the
User-Agent and the query header then report. Fix:
`$(shell (git describe --tags --always --dirty 2>/dev/null || echo dev) | sed 's/^v//')`.

## dtui's version is stamped into dtctl's pkg/version

`makefile` and `.goreleaser.yaml` stamp dtui's version, commit and date
into dtctl's `pkg/version`. dtctl's executor sends that `Version` in its
`dt-client-context` header under the app name `dtctl`, so the header gives
dtui's version as dtctl's. A build that passes no such flag (`wasm/build.sh`,
`go install`) sends dtctl's own default version instead. `Commit` and `Date`
are read only by dtctl's `cmd` package, which dtui does not link, yet
`ldflagProblems` in `invariants_test.go` requires all three. Open
decision, to take with whoever reads that header: stop stamping dtctl, so
the header names the dtctl that is linked, or stamp `Version` only. Fix:
after that decision, drop the unused flags and their requirement in the
test.

## The dtctl path guard reads text, not a build

`TestDtctlModulePathIsWiredEverywhere` checks how files spell dtctl's module
path. It cannot see what a build stamps: a recipe that loses `$(LDFLAGS)`
still passes. Fix: keep the text test, and add one assertion on the stamped
value to the snapshot build that `.github/workflows/ci.yml` already runs.

## Builds embed the builder's paths

Neither `wasm/build.sh` nor `.goreleaser.yaml` passes `-trimpath`, so
`dtui.wasm` and the release binaries carry the absolute paths of the
toolchain, the module cache and the checkout they were built from. For a
local wasm build that includes a home folder, and with it a user name. The
extension is loaded unpacked today and the file is git-ignored, so nothing
leaves the machine. Fix: decide `-trimpath` for both builds together,
before the extension is distributed.
