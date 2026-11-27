# ADR-0022: Exception issues are a client-derived, sampled grouping

**Status:** Proposed · **Date:** 2026-11-26

## Context

The exceptions lens on the traces view lists the newest spans that carry an
exception span event, one row per span, showing the first event's type and raw
message. The list is capped at the newest 200 spans, so one loud bug fills
every row and pushes the others out; nothing answers "which distinct problems
are there, and which is loudest".

Span exceptions carry no grouping key in the data. The type alone merges
unrelated bugs (every `NullPointerException` is one row), and the raw message
or stack splits one bug into thousands (ids in messages, line numbers that
move with every deploy, generated proxy class names). The fields a grouping
needs are free text inside `span.events`
([../dev/learnings.md](../dev/learnings.md) §1.11b).

## Decision

dtui derives an **issue key** for each exception-bearing span itself, in Go,
over a bounded sample. The first seven points say how the key is computed, the
rest how the view uses it.

- **The key is versioned.** `e1:` plus the first 12 hex characters of a
  SHA-256 over a canonical string of length-prefixed fields: the rung, the
  type, then the evidence (the parameterised message, or a module and a
  function per frame), so no two inputs can run together. It depends on no
  locale and no map order. It does depend on the Go toolchain's Unicode
  tables, which decide what the stack readers take for an identifier. Their
  version is pinned beside the digests, so a Go release that changes it fails
  the pin test (`TestExceptionKeyInputsArePinned`); if every corpus key still
  holds, that line is repinned without a new key version, and only if a corpus
  key changes is it a new key version. `e1` is frozen by the first dtui
  release that shows a key. From then on a change that can move an exception
  to another key ships as `e2`, and a change to a table the key reads is such
  a change: the vendor prefixes, the pass-through names, the message rules.
  Until that release `e1` may be repinned. Two golden files make the step a
  conscious one: the `e1` keys of a corpus, and one digest per declaration the
  key is computed from (a `const` block is one). The digests cover
  declarations; the code of a function is covered by the corpus and the tests
  alone. The readers and the corpus are the specification; this record names
  the rules and does not replace them. One vector for another implementation:
  an event with the type `é` and nothing else hashes the bytes `1:42:é` to
  `e1:c7bd5892e50a`.
- **One event per span is keyed.** Of a span's exception events that give a
  type, a message that is not empty once parameterised, or a frame a reader
  can use, take the last one flagged `exception.is_caused_by_root`, else the
  last one. Stack text no reader can use does not make an event count, and
  neither does a Go stack that is not a panic's, nor a message of which
  parameterising leaves nothing, such as one of control characters only. A
  span with no such event has no key. Where that event's stack text holds a
  whole chain (`Caused by:` in Java, an inner exception in .NET, an earlier
  traceback in Python, the first member of a Python exception group that is
  itself the first exception), the frames are the root cause's, read from the
  text, and so is the type where the text names one. A cause whose type cannot
  be read keeps the event's type, and a cause without frames is not the root:
  the nearest exception with frames is. A JavaScript `[cause]` chain is not
  split; its frames are read as one stack under the event's type.
- **A fallback ladder picks what is hashed.** In order:
  1. *app frames*: the type, the throw-site frame, and the application's
     frames below it that are not pass-through, until five frames that count
     are in. The throw site is one of the five when it is the application's
     own and not pass-through; otherwise it is in the key on top of them, so a
     key holds at most six frames. A frame is its module and function, never a
     line number. A frame that repeats the frame kept before it counts once,
     so direct recursion is one frame, also where only skipped frames lie
     between the two calls; `A, B, A` is three.
  2. *frames*, when no application frame counts: the type, the throw site and
     up to four frames below it, whatever they are.
  3. *message*, when no frame was read: the type and the parameterised
     message. Parameterising replaces the values in a message and keeps the
     codes that name the failure: a `SQLSTATE`, an Oracle-style code, and a
     number of up to five digits that follows HTTP, status or errno wording,
     or `code` behind the word that says whose it is (`error code`, `exit
     code`). So a 404 and a 500 stay apart on this rung, and a bare
     `code 12345` is a value.
  4. *type*: the type alone.

  Every row shows the rung that produced it, so a coarse group is visibly
  coarse.
- **A Go stack that is not a panic's is not used.** It was taken where the
  error was recorded, not where it arose, and every error that passes there
  has the same. Such an event is keyed as if it had no stack: on its message,
  else on its type alone.
- **The throw site is always in a frame key, and pass-through frames do not
  count among the application's.** Filter chains, middleware continuations,
  interceptors and decorator wrappers are shared by every error that passes
  through them; keyed on those alone, unrelated errors of one type merge. On
  one environment the top application frames of an error sent with a servlet
  `sendError` were two filter `doFilter` frames, not the throw site. They are
  skipped on the app-frames rung only. On the
  frames rung nothing is skipped, because there a handler's or a filter's own
  frame may be all that tells it from the next. A pass-through frame that is
  the throw site stays in the key, and an application's pass-through throw
  site with no other application frame below it still gives an app-frames key
  of that one frame. The lists are per notation, match a function name whole
  or without the type it is a method of, and leave out names an application
  commonly gives its own entry point (`invoke`, `step`, `dispatch`,
  `__call__`).
- **What counts as application code is built in and part of the key version.**
  There is no setting that replaces the rules and no second classifier, so
  every user computes the same key for the same event.
- **The key lives in a leaf package.** `internal/exckey` imports the standard
  library only (`TestExceptionKeyPackageIsALeaf`). The catalog may import it
  and nothing else of this module (`TestCatalogIsFrameworkFree`), so the key
  is testable without a tenant.
- **The view is a catalog `Spec` of its own, `:exceptions` with the aliases
  `:exc` and `:exception`.** It is not a lens of `:traces`: sampling belongs
  to a `Spec` ([ADR-0003](0003-declarative-view-catalog.md)), so a grouping
  lens would group every other lens with it. It is registered after events, so
  `:e` still opens events. It is a second list over the spans of the traces
  noun, not a new tab, a chart or a bespoke screen, which keeps it inside the
  depth cap of [ADR-0014](0014-breadth-not-depth.md).
- **One primary query; the grouping is done in the fetch command.** The query
  keeps the spans that carry an exception event, newest first. The catalog has
  a hook for this beside `Decode`, `Spec.Sample`: it names the record, the two
  limits and the function that groups. Fingerprinting and grouping run in the
  query's command, off the UI goroutine, and the view receives groups;
  `Decode` runs inside `tableView.Update` and must not parse a thousand
  stacks. The rows keep no span and no stack text, so the fetched spans are
  released with their stack text, and nothing is written into a record another
  goroutine can read. A result for a superseded request is dropped by its
  sequence number like any other; a running query is not cancelled. The unit
  is the span throughout: one key per span, from the one event the rules above
  pick, however many exception events the span carries. The exceptions lens
  keeps showing a span's first event, so the two can name different types for
  one span.
- **One query, with a bounded response.** One query per open meets the count
  of [ADR-0013](0013-query-budget.md); that ADR also bounds a query's shape,
  and an unprojected span comes with all of its `span.events` and full stack
  traces. So the query is projected to the fields the key and a row read and
  three more that the facet picker offers, it has a row limit, and the request
  sets an explicit result size limit. The mechanism is decided here. The
  numbers, 1000 rows and 8 MB, are provisional (see the open list). 1000 rows
  is the upper bound whatever the measurement says.
- **A group row is small, and the counts add up.** A row keeps its key, its
  rung, its count, its times, its services and the display fields of its
  newest member, not the member spans or their stack text. The view builds no
  list of a group's occurrences. A span that has no key (see "One event per
  span is keyed") is counted in one keyless row, labelled `(no key)` and
  always last, so the counts add up to the number the sample line gives.
- **A sample line says what the rows were computed from.** It is built from
  the same response as the rows. When nothing cut the sample it reads `all 3
  exception spans · last 2h`. A sample of one span has no "all" (`1 exception
  span · last 2h`), and an empty sample that nothing cut has no line. When
  something did cut it, the reason leads and the window trails, because a
  narrow table cuts the line at its end: the line starts `row limit: newest
  1000 exception spans back to 11:48:19`, then says `; any older ones are not
  counted`, then gives the window. A sample of exactly the row limit is
  reported as cut by it, because it cannot be told from a longer one. The
  reasons the line names are the row limit (`row limit:`), the result size
  limit (`size limit:`), a result Grail stopped at a consumption limit
  (`partial result:`), Grail's scan limit (`scan limit:`) and the fetch
  timeout. Where more than one applies the line names the gravest, a timeout
  or a scan limit before a consumption limit, that before a size or a row
  limit. The footer's notice follows the same order but reads only notices of
  severity warning or above, where the line reads every notice. So the two
  name the same reason when all notices are warnings, and can differ
  otherwise: an informational scan limit beside a size limit warning gives
  `scan limit:` on the line and the size limit in the footer, and a cut
  reported only as information gives a line and no footer notice. A response
  Grail marks as sampled is not called `all`: with no other reason the line
  starts `sampled read:` and counts the spans `in the part that was read`; an
  empty one says `no exception spans in the part that was read`. The reach
  (`back to`) is given only when the row or the size limit cut the sample and
  a span's time can be read: a loud bug can fill it in seconds, and a count
  alone hides that. After a scan limit or a timeout the spans are not known to
  be the newest, so the line says `of what was scanned` and gives no reach;
  after a consumption limit it says `of what was returned`. An empty sample
  that was cut never reads as "no exceptions": it says that the scan gave up,
  that no exception span fit the size limit, that `the result was cut and
  holds no exception spans` (row limit), or `no exception spans in what was
  returned` (consumption limit). Auto-refresh skips a tick while the view
  still waits for its sample, so a query slower than the interval is shown and
  not superseded, and auto-refresh pauses for as long as such a query is
  outstanding. A group's count is the number of sampled spans with its key, a
  count within the sample and not within the timeframe. Groups are ordered by
  count, then by newest span, then by key, so equal counts do not move between
  refreshes. This extends [ADR-0007](0007-scope-honesty.md) from scope to
  completeness.
- **Facets and search act on spans; the key is local.** A facet narrows the
  query before sampling, so it uses fields of the fetched spans, collected
  before grouping. The issue is stamped on the row under a `__` key
  ([ADR-0016](0016-detail-panes-show-real-grail-keys.md)) and is never offered
  as a facet or sent to the server. `/` filters the visible groups and matches
  the key, the full type, the type the event recorded and every service of the
  group as well as the cells. On this view Enter keeps that filter local,
  since a server `search` would look for a key no span holds, and `ctrl+s`
  sends the text as a server search over the spans, added to the terms already
  there; `alt+enter` replaces those terms with it, as on every view.
  `--search` and a preset's searches stay server-side.
- **A row opens its newest span, and the exits hand over the query.** Enter
  opens the trace of the group's newest span, and `l` the logs of that trace.
  `y` yanks the key. `o` opens the primary query in a Notebook and `c` echoes
  it. Both hand over the query that fetched the sample, not the sample: run
  again it returns the spans of that moment, one by one and ungrouped, because
  no DQL computes the key, and both say so. The Notebook link holds the query
  text and nothing else, so neither the session's segments nor the result size
  limit go with it; the echo carries both, the limit as `--max-result-bytes`.
  That is a stated limit on the two exits of
  [ADR-0012](0012-navigate-analyze-mutate.md), not a third exit and not an
  override of [ADR-0001](0001-dql-is-the-substrate.md): what is handed over is
  still DQL.

Open, to be settled before this is accepted. This record stays Proposed, and
its pull request open, until this list is empty:

- The row limit and the result size limit as numbers. 1000 rows and 8 MB are
  provisional: they were chosen from the size of single spans on an
  environment with very few exception spans and have not been measured on one
  that fills them. One measurement of a full sample on a busy environment
  confirms or changes them before anything merges: bytes scanned, bytes
  returned and time to first rows, against the budgets in
  [vision.md](../design/vision.md) and against the shortest auto-refresh
  interval, 10 s. The result size limit is approximate, a result can exceed
  it, and the measurement says by how much. The numbers are then written into
  this record.

The order of landing is this record, then the key, then the view. The key and
the view are held until this record is Accepted, and the key does not merge
before its code follows "One event per span is keyed", because the first
release that shows a key freezes `e1`.

Left undecided on purpose. Nothing above depends on these, and none of them
needs a further ADR:

- Exact counts over the whole timeframe. They need a server-side key that
  every member of a group shares, and the event's `exception.type` is not one:
  a wrapped and an unwrapped throw of one root cause share a key and differ in
  type.
- Whether RUM errors use their own `error.id` instead of this key. That field
  has not been checked on a live environment.
- Whether the code eventually lives in dtctl's SDK, so other tools compute the
  same key. It is a leaf package for that reason; moving it means the import
  allowlist of `internal/tui` gains the SDK package.

## Rejected alternatives

- **Grouping in DQL.** DQL cannot pick the root of a cause chain across a
  span's events, read frames out of five stack notations or drop pass-through
  frames. A coarser grouping on the server, by the first event's type and the
  service, would give exact counts over the whole timeframe and a far smaller
  response, and it merges every bug of one type. Its cost was not measured.
- **A lens of `:traces`.** See "The view is a catalog `Spec` of its own".
- **A setting that replaces the in-app rules.** Keys would stop matching
  between users, and a key that carried the classifier's identity would still
  look like any other.
- **Type only.** Unrelated bugs that throw the same type become one issue.
- **The full normalised stack.** One bug reached through two callers becomes
  two issues.
- **Hashing line numbers.** Every deploy that moves a line starts a new issue
  for an old bug.

## Consequences

- Issue keys are stable across refreshes, sessions and machines for the same
  key version, but they are dtui's, not the tenant's: nothing in Grail can be
  filtered by one.
- An `e1` key copied into a ticket stops matching once `e2` ships. dtui stores
  no key of its own accord, so nothing migrates. One place keeps a key the
  user typed: the page history stores a view's `/` filter, and a key typed
  there is restored with the page and matches nothing after `e2`. Keeping it
  out of the history is owed by the view. If Grail gains a grouping key for
  exceptions, this ADR is superseded.
- A rare exception outside the sample is absent, not counted as zero; the
  sample line says how far back the sample reaches, and narrowing the scope or
  the timeframe is how the user reaches it.
- The command echo (`c`) is `dtctl query` with the view's primary query. Run
  later it returns the spans that match then, ungrouped, which need not be
  the spans on screen. The keys, rungs and counts are computed in Go and no
  DQL reproduces them, so the echo hands over the input of the grouping, not
  the grouping.
- Grouping quality depends on the stack readers. A notation none of them reads
  (Rust, Swift, PHP, Ruby) yields no frames and is keyed on the message rung
  rather than failing. The pull request that adds the key lists what the
  readers are known not to read in [../dev/tech-debt.md](../dev/tech-debt.md).
- The table engine gains one hook, `Spec.Sample`, and a line above the table
  for a sampled view. Any later view that groups a bounded sample uses the
  same hook.
- This is a new subsystem (five stack readers, a message parameteriser, a
  versioned key), which [vision.md](../design/vision.md) answers with no by
  default; this record is the override, for the reason in Context. Against the
  gate: it shows what is happening, reads only, costs one primary query, is a
  catalog `Spec` and needs no configuration. At its depth cap it hands over
  the query of the sample, not the grouping.
- The golden files of the key are not layout snapshots.
  [CONTRIBUTING.md](../../CONTRIBUTING.md) allows them as versioned
  compatibility fixtures.
- The view's tests cover: a sample of exactly the row limit, which is
  reported as cut; a sample one short of the row limit, which is not; a
  sample of one span; a result cut by the size limit,
  by the scan limit and by a timeout, each named as that; an empty cut sample;
  typing a key, pressing Enter and asserting that no DQL holds it; the facet
  picker's fields taken from the spans; the layout at four sizes.
