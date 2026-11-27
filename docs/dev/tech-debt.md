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

## The exceptions view's sample caps are not measured

`ExceptionSampleRows` (1000) and `ExceptionSampleBytes` (8 MB) in
`internal/tui/catalog/excissues.go` are unmeasured on a busy environment.
They were set from the size of single spans: the only live environment at
hand held four exception spans, so no full sample was ever fetched, no
response cut by either limit was ever seen with its real notices, and the
grouping was checked on synthetic spans only. The view's opening time and
memory are these two numbers. ADR-0022 requires the measurement before
merge and stays Proposed until then. What to measure on an environment
that fills the sample:

- bytes scanned, bytes returned and the time to rows for 24 hours, 7 days
  and 30 days;
- one response cut by the row limit and one cut by the size limit, with the
  notices Grail sends for each;
- the grouping time per runtime, not for Java alone: 1000 spans of one
  70-frame stack take about 17 ms for a Java stack and about 200 ms for a
  JavaScript one; .NET and Go stacks are not measured;
- the browser build, where the grouping shares the one thread that draws.

Then settle both limits and record the result in `learnings.md` §1.11d.

## The exceptions view computes each span's message for the key

`exckey.FingerprintExceptions` parameterises the message of every span,
though only the newest span's message of a group is shown and the key reads
it on the message rung alone: about a fifth of the fingerprint's time on a
span with a stack. It is off the update loop, so it costs fetch time, not
frames. Fix: let the fingerprint skip the message where the key has frames
and compute it once per group.

## Two ways to echo a query

`dataSource.echoQuery` folds a query's whitespace and quotes neither the
query nor a segment id; `echoQueryExact` (the exceptions view) goes through
`catalog.EchoCommand`. The snapshots echo in
`internal/tui/catalog/livedebugger.go` formats its argument into the
command the same unquoted way. Fix: route
`echoQuery` and the snapshots echo through `EchoCommand` and delete
`echoQueryExact`.

## A superseded result still sets the footer notice

`app.Update` reads the footer's partial-result notice from any `dataMsg`
the top view owns, before the view drops a result whose sequence number is
stale: a slow clean answer to an earlier refresh clears the warning about
the cut result on screen, and a slow cut one warns about a clean result.
On the exceptions view the line above the table stays right, because its
facts travel with its rows. Two more paths leave the footer wrong: `esc`
clears the notice and the view it returns to does not get its own back, and
`-` carries the notice of the view it leaves over to the other one. Fix: let
the view say whether it accepted the result and take the notice only then,
and keep the notice with the view it belongs to.

## The exceptions view offers no bucket facet

The view's projection (`SampleSpec.Fields`) drops `dt.system.bucket`, so
`composeDQL` does not project the bucket on a sampled view and
`orderCandidates` does not pin it, though a bucket filter in front of the
sort would narrow the scan before sampling. A bucket facet that reaches
the view another way (typed in the facet manager, or from a preset) is
composed as a late filter for the same reason, not as the `bucket:`
parameter; what scan pruning that loses is unmeasured. Fix: offer the facet
on a sampled view without projecting the field, once a bucket-filtered
sample was checked on a live environment.

## The value picker's counts on a sampled view are not the table's

The picker's top values come from a `fieldsSummary` query that runs before
the sample's limit, so on `:exceptions` its counts are matching spans in
the whole timeframe, beside a table that counts the newest 1000. Nothing
labels them. The picker is shared by every view. Fix: say in the picker
what its counts are counts of.

## A facet from another view is refused for a field the projection drops

A facet sent from a span inspector onto `:exceptions` is accepted only for
a field the sampled records carry (`hasField`). The projection keeps few,
so a facet on `k8s.namespace.name` is refused with "exceptions rows carry
no k8s.namespace.name field", though the filter would land before the
projection and work. The guard cannot know which fields a span has without
the full record. Fix: on a sampled view accept any field and let an empty
result speak, or ask the source for the field's presence.

## An exception issue has no detail page

`d` on an exceptions row opens the inspector on the row's three real fields
(the newest span's ids and start time); what the key was built from is in
the preview pane only, which narrow or short terminals hide. Fix: a detail
view of the issue, or a `KeyFacts`-style block in the inspector.

## A failed refresh leaves the previous result live

When a refresh fails, a table shows the error and keeps the rows of the
last good result with their count, elapsed time, preview pane and row
actions, on every table view. Nothing marks them as older than the error.
Fix: mark the kept rows as stale, or drop the count and the elapsed time
with the result they described.

## A handoff does not carry everything the session queries under

The copied command (`c`) and the Notebook link (`o`) carry the query and,
for the command, the segment ids. Neither carries the session's context
name or the variable bindings of a segment, and the Notebook link carries
no segment at all, on every view, so the handoff can address another
population than the screen. dtctl has `--context` and `-V` for the first
two. Fix: add them to the echo, and say next to the Notebook link what it
leaves out.

## Clock times are local beside a window labelled in UTC

`FormatTime` prints local time, while a custom timeframe's label is in
UTC, so the exceptions sample line can read "back to 11:48:19" before a
window "10:00→12:00 UTC" that does not contain it, and every table has the
same pair in its header and cells. Fix: one zone for both, app-wide.

## A superseded query is never cancelled

A refresh that a newer one supersedes, and a query of a view that left the
stack, run to their end: on `:exceptions` up to 8 MB still downloads, is
grouped and held until the result is dropped, and the client waits up to
two minutes for the execute and five for the poll. The comment in
`datasource.go` gives as the reason that the executor prints a
cancellation notice to stderr; since dtctl v0.45.0 `DQLExecutor.WithStreams`
can keep that off the screen, and the sdk cancels the backend query when
the context ends. Fix: `WithStreams(io.Discard, io.Discard)` on the
executor and a context per view, cancelled when a newer refresh starts or
the view leaves the stack.

## dtui ignores the query limits configured for dtctl

dtctl applies the limits a user configured (`EffectiveQueryLimits`: scan
limit, result bytes) in its own command only; dtui reads none of them, on
any view. Fix: read them where the executor options are built
(`dataSource.execOpts`), below the limits a view sets itself.

## An empty waterfall under segments is fetched twice

A waterfall whose first fetch is empty without a partial notice widens once:
to 12 hours either side of its anchor, or to 24 hours without one
(`waterfall.go`, `Update`). Segments ride on both queries, so a trace the
active segments exclude costs a second, far wider span scan for a result
that stays empty. Fix: do not widen when the first query ran under
segments, or retry without them and say so.

## learnings.md is one long file

`docs/dev/learnings.md` is over 1,700 lines, so an agent looking for one fact
loads all of it or greps. Fix: keep the section numbering stable and, if it
keeps growing, split section 1 (DQL facts) by data object.

## Exception keys: what the fingerprint does not read

`internal/exckey` groups exceptions by a key. Known gaps, each
of which splits or merges issues it should not:

- A JavaScript `[cause]:` chain in one stack text is not followed; all
  frames of the text are taken as one exception's (`jsRootCause`).
- A route inside a URL is replaced with the URL (`<url>`), so two failing
  endpoints of one message merge; a bare route (`GET /cart/7/items`) is kept.
- A Go dependency counts as application code when its trace has no file
  lines and its host is not in `goVendorPrefixes`; an application package
  named like a standard-library one counts as the runtime's.
- A Go main module, or a sibling module under `replace`, that the trace
  prints as `module@version` (a `-trimpath` build, an install by version)
  is taken for a dependency. The cure is to learn the main module from the
  file line of `main.main`.
- A `pkg/errors` style Go trace (a bare `main.run` over a file line, no
  arguments) yields no frames and falls to the message rung.
- A segment of a Windows path with more than three words ends the path at
  its first space, and so does the last segment (`C:\data\my cart.json`).
- The pass-through lists (`javaPassThrough` and its siblings) match by
  method name alone, so an application method named `proceed` or `next`
  never counts toward a key next to other application frames unless it is
  the throw site.
- A cause chain is found by `exception.is_caused_by_root` alone.
  `exception.caused_by_id` is not read, since no live span seen so far
  carried it, and `exception.escaped` has no role: instrumentation that
  links its events without flagging the root is keyed on its last event.
- A snake-case code (`error_code_404`) loses its number to the id rule,
  which runs before the code rule: `error_code_404` and `error_code_500`
  merge.
- Messages: a unit that is not on the list (`250milliseconds`, `msgUnits`),
  an IPv6 zone id and `host:addr`, punctuation directly after a URL, a
  status inside a JSON body (`{"status":404}`), and a message that crosses
  the 4096-byte input cap are not read as what they are.
- Messages: an id is replaced by its form. One that mixes cases or is no
  hex run of seven characters (`A1B2C3`, `req-8Hk2Lm9Q`, `12ab`) is kept, and
  so is a decimal glued to a word (`1.5foo`), so such a message has a key
  per value. Treating every word that mixes letters
  and digits as a value would also take `sha256`, `http2` and `x86`.
- Messages, the wording of a code: a hex code (`error code 0x80070005`), a
  code in brackets after an equals sign (`status = (404)`), `gRPC code 4`,
  `ORA-942` and a code of six digits are values and are replaced.
- Messages, quotes: a single-quoted value that a letter follows directly is
  not closed there.
- Messages, paths: a UNC path (`\\server\share\f.pdf`) and a relative path
  of one segment (`./cart.json`) are kept.
- Messages, cost: 4 KB of numbers takes about 1.5 ms in the number rule, a
  hundred times the cost of prose. No key depends on it.
- The message is parameterised for every event that is keyed, although
  only the message rung hashes it: `ExceptionFingerprint.Message` is set on
  every rung, which is the documented shape of the result. A caller that
  does not show it pays for it all the same.
- JavaScript: Safari's `global code` and a `fn@url` frame with a line but no
  column are dropped. A V8 frame without a function whose Windows path
  holds a space (`at C:\Program Files\app\index.js:10:5`) is dropped, and
  so is a location that starts with a digit. A bare `cart.js` is taken for
  one of Node's own files (the cure is Node's list of core modules). A
  colon and digits inside a file name are cut as a line number, and a file
  URL is not percent-decoded.
- JavaScript, Firefox and Safari: an escaped trace is split only where
  indentation follows a break, which their traces do not have.
- A stack text that is prose alone can read as one frame
  (`client.connect(redis.internal:6379)`) and then gives an app-frames key.
- .NET: every input is written by hand, in the corpus and in the tests; no
  .NET runtime was at hand. Owed before the first release: real traces of
  an async method, a lambda in a generic method, an explicit interface
  implementation (the hyphen in its generated name), an `AggregateException`
  with two inner exceptions, and Mono.
- .NET: a type argument that is itself generic leaves `]` in the module
  (`dotnetTypeArgsRe`); no runtime was shown to print instantiated type
  arguments. F# frame names (`main@12-1.Invoke`) are dropped. A Mono frame
  with a generic parameter of two type arguments and no offset is read as
  JavaScript. Localised traces (`bei`, `à` for `at`) are not read.
- .NET: a qualified word after an arrow at the end of a message line
  (`---> Shop.State.Pending`) is read as an inner type. The closing line
  could confirm a real inner exception, at the price of the inner type of a
  trace that was cut.
- Go: a `panic: ` line that a wrapper wrote above the output of
  `debug.Stack` makes a trace that is no panic's count as one; its frames
  then start at `runtime/debug.Stack`.
- Go: `github.com/gin-gonic/gin`, `github.com/gorilla/mux`,
  `github.com/sirupsen/logrus` and `k8s.io/client-go` are in no vendor
  table and count as the application's in a trace without file lines.
  `org.junit.` is not in the Java table.
- Python: a frozen standard-library module (`<frozen os>`) and the same
  module read from a file (`lib/python/os.py`) are two names.
- Modules: a release directory of hex letters only (`/app/deadbeef/src`) is
  kept, since a word of hex letters is a word; `<app>/current/` below a
  shared root keeps `<app>/current/`; the directories of a platform's
  runtime (`/var/runtime/`) count as the application's.
- No corpus case shows what a trace cut by the instrumentation falls back
  to (the OpenTelemetry Go SDK keeps 2,048 bytes).
- The inputs pin sees declarations, not the code of functions: a single
  string inside a function body (`"created by "`, `" in goroutine "`,
  `"<module>"`, `"jdk.proxy."`, `"lambda_method"`) is a key input only a
  case covers.
- In the browser build Go has one thread, so fingerprinting a full sample
  blocks input and paint: about 1 ms per 70-frame JavaScript trace under
  `js/wasm`. The view that calls the fingerprint has to work in bounded
  batches that yield, or in a worker.
- For the first release, to be decided then and written into ADR-0022:
  a run of the old and the new binary over the same traces before a key
  version is raised, a CI check that a released line of a keys golden never
  changes, and what happens to the golden files of a retired version, which
  nothing reads.
- Java: nested lambdas (`lambda$lambda$addItem$0$1`), Scala 3 anonymous
  functions (`$anonfun$1`) and a JPMS module version with `+` keep an
  ordinal or drop the frame. Needs real traces before a rule.
- Modules: a base64 bundle hash (`Cart-B4xK9pQ2.js`), a dotted Windows
  Python directory (`C:\Python3.11\Lib`), a `#fragment`, a repeated `/./`
  and a lower-case Windows root (`/users/deploy`) are kept as written.
- The fuzz targets check that a key is well-formed and repeatable, not
  that it depends on its input: a fingerprint that returned one constant
  key would pass them.

Fix: each needs a corpus case first. Until a release has shipped `e1` keys
the cure is free; after that each one moves keys and so costs a key version
(`ExceptionKeyVersion`).
