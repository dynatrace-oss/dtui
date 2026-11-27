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
