# ADR-0021: Repo scope is dtctl's file; dtui applies it and never writes it

**Status:** Proposed · **Date:** 2026-11-26

## Context

dtctl is adding an experimental repo scope: a committed
`.dtctl-repo-scope.yaml` at the git root, never looked up past `.git`, keyed
by environment host. An entry binds a directory of the repository to what runs
its code: Kubernetes workloads (a namespace and a name, optionally one
cluster), service names, process groups and services. An entry may also
reference existing filter segments by id, with values for their variables, and
may hold nothing else. The file holds bindings and references, never DQL:
dtctl renders the filter itself, with every literal validated and quoted, and
segment ids and values travel in the request body. A cloned repository can
narrow a query but cannot widen it or put text into it.

A user who launches dtui inside such a repository expects the same narrowing
dtctl gives them there. dtui already has one committed narrowing, the
`segments` of `.dtui.yaml`, and one rule for claiming any of them
([ADR-0007](0007-scope-honesty.md)). dtctl exports the read path:
`pkg/reposcope` locates, loads and resolves the file and renders an entry's
filter, and `InsertFilter` of `pkg/dql` puts that filter into a query and says
whether it did or why not. None of this writes or reaches an environment. The
same package also exports what dtui does not use: functions that change the
file, discovery, which runs queries, and a rendering for the Live Debugger.

dtctl's package is experimental and may still change. Its design document is
`docs/dev/REPO_SCOPE_DESIGN.md` in the dtctl repository; what this record
quotes from dtctl was read there and in the code at commit `e4d4ec1b` of the
branch that adds the feature, before any release. This record therefore
states the rules dtui follows and names a dtctl function or field only where a
rule needs one. Where a detail here and dtctl later differ, what dtctl does
with the same file in the same directory is the reference.

The scope file sits beside `.dtui.yaml`, whose segments are another
maintainer's feature. What this record says about how the two meet is a
proposal to the owner of that feature, not an agreement; see "Open questions".

## Decision

The scope file is dtctl's. dtui applies the entry dtctl resolves and owns
nothing about the format.

- **Read in the main package, once per context.** `internal/tui` may import
  only dtctl's `pkg/exec` and `pkg/output` (`TestTUIHasNoDirectTenantAccess`),
  and that allowlist stays as it is, so no type of `pkg/dql` or
  `pkg/reposcope` crosses into `internal/tui`. The main package locates and
  loads the file from the launch directory and resolves it for the host of the
  selected context, derived as dtctl derives it (the lowercase hostname,
  without port, path or a trailing dot), not with dtui's own `envKey`. It
  hands `internal/tui` a value of a type `internal/tui` declares, as a message
  after the first paint, at launch as at a switch: the entry's
  name, its segments as `exec.FilterSegmentRef`, a function from a composed
  query to the rewritten query with an outcome and a reason, and the warning
  to show. The value is built again on every context switch and travels on
  `ContextWiring`, so a scope and the executor it belongs to are replaced
  together, and a context whose host has no entry clears it. A query is sent
  with the executor, the segments and the scope it was composed under, also
  when a switch arrives before it is sent, and a reply that belongs to the
  previous context (a segment list, a segment check) is dropped. Today a query
  command reads its executor when it runs and a segment list reply is not
  checked for its context, so the implementing pull request changes both. A
  switch-off chosen at launch survives the switch. Resolving can walk the
  repository (dtctl reads a Gradle build's modules for a file with a path
  entry), so it runs once per launch and once per context switch, never per
  query, and not on the UI goroutine: the shell paints first, and a view the
  scope can narrow sends its first query when the scope is known, so no
  unscoped rows are shown first. Until then such a view shows its loading
  state. That differs from the segments of `.dtui.yaml`, whose first queries
  go out unsegmented and are superseded. An edit of the scope file takes
  effect at the next launch or context switch. The wait, and the segment check
  where the entry has segments, come before the first rows, which
  [vision.md](../design/vision.md) budgets at about 2 s; the implementing pull
  request measures them against it. The browser build (`cmd/web`) has no file
  system and passes no scope. dtui does not re-implement the lookup, the
  renderer or the insertion.
- **The entry's filter narrows the logs and spans lists.** The rewrite is
  applied to the composed query of `:logs` and `:traces`, after the lens, the
  facets and the searches are in it. The pattern analysis (`a`) takes that
  list's query text as it was sent, with no whitespace folded, so it carries
  the entry's filter where one was inserted. The analyzer request dtui sends
  carries no segments. On a list narrowed by segments, the entry's or the
  user's, the analysis therefore says in its header that segments do not
  narrow it, and an entry that holds only segments narrows the list and not
  the analysis. The facet value query (`f`) takes the list's outcome, applied
  or not, and is not decided again from its own text: it leaves out the facet
  being edited, so decided alone it would get the entry's filter while the
  user edits a facet on a bound field, and hide the values they came for. The
  `:exceptions` list of ADR-0022, once it exists, is a second list over the
  spans and its query is rewritten as that of `:traces` is. The entry's filter
  is not applied to the trace waterfall, the home panels, `:agentlogs`,
  `:records` or a segment's variable query. An entry's segments are sent with
  the waterfall's query as with every query, so a trace opened under them can
  show only the spans they admit, a span whose parent they exclude is drawn as
  a root, and a waterfall they empty is fetched once more over a wider window,
  as today.
- **DQL the user hands to dtui is scoped as dtctl scopes it.** `:query`,
  `dtui query <dql>`, `--dql` and `-f` go through the same rewrite, with the
  claim or the reason for no claim in the header. An agent hands off the text
  it ran through `dtctl query` in the same checkout, and dtctl scoped that
  text; dtui showing other rows for it would break the handoff of
  [ADR-0018](0018-launch-flags-are-a-handoff-contract.md). Text revealed with
  `ctrl+q` is not rewritten: it is the page's own query made visible, it
  already holds the filter where one was inserted, and it runs as shown. From
  there on it is text. The page history and the query history store it with
  the inserted filter in it, and a text restored or recalled from either goes
  through the same rule as typed DQL, in whatever checkout it is opened, with
  the reason in the header when the entry's filter is not inserted.
- **A page the user scoped is not narrowed again.** A `:logs` or `:traces`
  page that carries an entity (a pin, a drill from a row, a tab of an entity
  or problem page) or a trace id runs without the entry's filter, and its
  header says so. The entity or the trace is the user's own narrowing and
  wins, as their filter on a bound field does. Left to the field names in the
  query, the outcome would depend on which fields a pin happens to write: a
  pinned process group and a named workload would refuse the filter while a
  pinned process, a namespace without a resolved name and every `:traces` pin
  would be ANDed with it, mostly to an empty page. Nobody can predict that
  from the screen. The entry's segments are still sent on such a page, as the
  workspace's are today, so a pin on something they do not admit shows no
  rows; the header then names the entry's segments and says that the filter
  was left out.
- **Otherwise `InsertFilter` decides.** It leaves a query unchanged when one
  of the query's own `filter` or `filterOut` stages names a field the entry
  binds for that data object, such as `service.name` or `k8s.workload.name`;
  dtctl's design document lists the fields per data object, and a field counts
  only when the entry has a binding of that kind. A facet on one of those
  fields therefore runs alone, and so does a map facet whose key is such a
  name. A lens, a `/` search and a facet on any other field are ANDed with the
  entry's filter. dtctl's other refusals leave the query unchanged too and are
  reported the same way: a first stage that does not fetch logs or spans, an
  entry without a binding for the data object, a subquery that fetches logs or
  spans again, and text dtctl's scanner cannot vouch for.
- **`:traces` opens on `all` under an applied filter.** Root spans belong to a
  trace's entry service, so `roots` ANDed with the entry is empty for most
  services (`DefaultSpanLens` records this for entity scopes). When the filter
  applies and no lens was named, `:traces` opens on the lens `DefaultSpanLens`
  gives a service. `--lens` and a lens the user picks are kept. An entry that
  holds only segments inserts no filter and leaves the default lens as it is.
- **An entry's segments are part of the repo scope.** They are sent with every
  DQL query of the session, as the segments of `.dtui.yaml` and of the picker
  are, the session's lookup queries included; an entry that holds only
  segments inserts no filter and narrows every such view. This is dtctl's
  rule: one entry, one switch. They are sent together with the user's: a
  segment both name is sent once, with the user's variables, because what the
  user chose wins over what is committed. More than ten in total is the
  platform's limit; the entry is then not applied at all and the header says
  why, since dropping some segments would change the rows unseen. The limit is
  counted over the user's selection whether it is paused or not, so pausing
  never switches the entry on. They belong to the scope, not to the selection:
  `alt+s` pauses the user's segments only, the picker shows the entry's as
  locked rows, and the switch for the scope switches them with the filter.
  While `alt+s` pauses the user's segments, a segment both name is sent as the
  entry's, with the entry's variables, and with the user's again when the
  pause ends. Before a scoped query is sent dtui checks, once per context and
  not per view or per query, that the environment returns each of the entry's
  segments to this user. If one is missing or not readable, the whole entry is
  not applied, filter included: half a scope shows rows that look scoped. If
  the check itself fails, the entry is not applied either, the header says the
  check failed, and the user's own refresh and a context switch try again. An
  auto-refresh tick does not, and one check is in flight per context. dtctl
  fails the command in that last case; dtui shows the page, because a header
  is read on every page where a script reads only an exit code.
- **The workspace's segments stay independent.** `.dtui.yaml` segments and the
  entry's filter are ANDed and neither replaces the other.
- **Claim what was sent.** The header names the entry on every view whose
  request carried its filter, its segments or both, and says which
  ([ADR-0007](0007-scope-honesty.md)). A `:logs` or `:traces` page whose
  filter was not inserted says why for as long as the page is shown, not in
  the status line, which the next key clears. The claim is taken from the
  request that produced the rows on screen.
- **On by default, never fatal.** When an entry resolves, the scope is on.
  dtctl applies it by default in the same directory, and two tools that
  disagree about one query are worse than a default. Nothing is said when the
  directory is in no git repository, the repository has no scope file, or the
  file has no entry for this host. A file that does not load (an unknown key
  inside an entry is one such case), a directory no entry covers, a tie
  between entries and an unknown key of the document are one warning each,
  never a failed launch, like a broken `.dtui.yaml`. dtui words the warnings
  itself from the resolved status: dtctl's reasons name `--repo-scope <name>`,
  a flag dtui does not have.
- **One switch for the whole entry, and it is session state.** A
  `--no-repo-scope` launch flag, or `DTCTL_NO_REPO_SCOPE` read as dtctl reads
  it (empty, `0`, `false`, `no` and `off` do not switch anything off), turns
  the entry off for the session, segments included. While dtui runs, a
  command-bar command switches it and refetches the views it reaches. It gets
  no key: under [ADR-0008](0008-one-owner-per-key.md) a key has one owner, and
  an experimental feature of another tool is not worth one. The command's name
  is the implementing pull request's to choose. A page pushed by `dtui send`
  takes the scope of the listening session, on or off, and the wire format
  gains no field for it: a request never changes what the user chose for the
  session ([ADR-0019](0019-agents-may-navigate-a-listening-tui.md)), and the
  scope is replaced with the context, which a request cannot change either.
- **Not in the first version: naming an entry.** dtui has no
  `--repo-scope <name>`. A directory that no entry covers, or that two entries
  tie on, has no scope in dtui, and the warning says which directories the
  entries cover.
- **dtctl's stability floor is not read.** The floor governs which flags a
  dtctl command accepts. dtui is not a dtctl command and has its own switch.
  The cost is in Consequences.
- **`--no-repo-scope` is outside the launch-flag contract.**
  [ADR-0018](0018-launch-flags-are-a-handoff-contract.md) makes the flags it
  lists stable, and dtctl's feature, its flag and its variable are
  experimental. Promising the flag there would promise more than dtctl does.
  It joins the contract, or not, when dtctl's feature is stable.
- **It never selects the environment.** `.dtui.yaml`'s `environment` and
  `--context` keep that job; the scope file is only consulted for the host
  they selected.
- **Read-only.** dtui never calls the functions that change the file, never
  runs discovery and does not use the Live Debugger rendering. It reads
  segments, as it already does ([ADR-0011](0011-strictly-read-only.md)).
  Creating and editing entries stays in dtctl.

For example, a repository whose file has an entry `checkout` for
`abc12345.apps.example.invalid` covering `services/checkout`: dtui launched in
that directory on that environment opens `:logs` and `:traces` narrowed to
`checkout`'s bindings, `:traces` on the `all` lens. If the entry also
references segments, every DQL view carries them and names the entry in its
header. A view that carries neither shows no scope.

## Rejected alternatives

- **A dtui-internal parser of the scope file.** The renderer is
  security-relevant, and a second copy would drift from dtctl's.
- **Rendering the entry as facets.** An entry is an OR across fields; facets
  are single-field and ANDed.
- **Shelling out to `dtctl repo-scope current`.** dtui links dtctl's packages
  and never invokes the binary ([ADR-0005](0005-dtctl-owns-configuration.md)).
- **Opt-in only.** dtctl applies the scope by default in the same directory,
  so the two tools would disagree about the same query until the user found a
  flag.
- **Merging the entries into `.dtui.yaml`.** That file walks to the filesystem
  root and ignores unknown keys; the scope file stops at `.git`, is not
  applied at all when an entry holds a key dtctl does not know, and is not
  rewritten by dtctl when the document does.
- **dtctl reading `.dtui.yaml` on its own.** A committed file would then
  choose the environment a mutating CLI talks to.
- **Treating the entry's segments as workspace segments.** `alt+s` and the
  picker would then switch them apart from the filter they were written with,
  and dtctl has one switch for the whole entry.
- **Letting the field names decide on a pinned page.** See "A page the user
  scoped is not narrowed again".
- **`--no-repo-scope` in the command echo.** The flag is refused under a
  stable floor and unknown to a dtctl older than the feature, and it was only
  put on scoped views, while an unscoped page's echo pasted in the checkout is
  the one dtctl would scope.

## Consequences

- dtui needs a dtctl release that contains `pkg/reposcope` (first planned for
  0.47.0) and the move to the `github.com/Dynatrace/dtctl` module path.
- dtui couples to an experimental dtctl package: its API may change in any
  dtctl release, and a dtctl bump can break the build.
- The command echo (`c`) of a DQL view names the request dtui sent: the query
  text, rewritten where the filter was inserted, one `-S` per segment sent,
  variables included, in the spelling dtctl's `--segment` takes, and
  `--context <name>` when the session's context is not the one dtctl would
  use. dtui's context is session-local (`--context`, `DTCTL_CONTEXT` or the
  workspace file), so without it a pasted command runs on dtctl's current
  context. `o` hands over the same query text, the one that was sent, on every
  view the rewrite reaches; today `o` and `ctrl+q` on `:query` read the text
  as typed. Every argument is quoted for a POSIX shell by one helper of the
  catalog, which every echo goes through, and no whitespace inside the query
  is folded: a service name may hold an apostrophe or two spaces, and a
  segment's variables are joined with `&`. Today's `echoQuery` wraps the text
  in bare single quotes and folds whitespace, which would let a committed name
  end the quoting and run the rest as shell commands. It is corrected before
  any code reads the scope file.
- While an entry resolves for the session's context, every `dtctl query` echo
  starts with `DTCTL_NO_REPO_SCOPE=1 `, whether the view was scoped or not and
  whether the scope is switched on or off. dtctl run in that directory would
  otherwise apply the entry to a query dtui ran without it, or add the entry's
  segments to a command that already names them. The variable works under
  every stability floor and is ignored by a dtctl older than the feature; the
  flag does neither. The prefix is POSIX shell syntax, so the echo is not a
  command for other shells. The catalog invariant that every view's echo
  starts with `dtctl ` (`TestEveryViewHasBothExits`,
  [ADR-0012](0012-navigate-analyze-mutate.md)) is restated: the echo is a
  dtctl command, behind the variable where an entry resolves.
- dtui scopes by default where dtctl, under a stability floor that hides the
  experimental flags, does not. The header says what dtui sent, and the echo's
  prefix makes the pasted command send the same request in the same context.
- The entry's filter narrows the rows and helps the scan, but it cannot by
  itself keep a wide timeframe under Grail's scan limit (dtctl's design
  document records a scoped 15-minute log count that still scanned 341 GB), so
  a scoped view can meet the limit as an unscoped one does, and the existing
  partial-result notice says so.
- Presets (`W`, `send --save`), the last filter and history record facets and
  searches, as they do today with segments active. They do not record the repo
  scope: a preset saved in one checkout opens under whatever scope the session
  that opens it has. The one exception is query text: a revealed query holds
  the inserted filter as text, and the two histories keep it that way. Today
  the page history also stores a draft that was never submitted and runs it
  when the page is restored, which the implementing pull request has to take
  into account.
- `ctrl+q` puts the rewritten query into an editor that holds 4096 characters,
  and an entry with many ids renders a longer filter. The reveal must report a
  query it cannot hold instead of cutting it.
- The pull request that implements this owes tests for: the echo of a name
  with an apostrophe and of one with two spaces, parsed back by a shell into
  the query that was sent; `TestEveryViewHasBothExits` with a resolved entry;
  the claim and the reason on an 80 column screen, where the header line is
  cut when it does not fit; a failed segment check followed by an
  auto-refresh tick, asserting that no second check is sent; eight segments
  of the user and three of the entry with the user's paused; `o` on `:query`
  under an applied filter; a context switch from one host to a second and on
  to a host without an entry, asserting the request each executor receives; a
  command of the first context released after the switch, asserting that the
  second executor receives nothing; an entry that holds only segments; an
  entry whose filter is refused while its segments are sent; one segment id
  named by the entry and by the user with different variables, and the
  roster's dimensions after `alt+s` paused the user's; eleven segments in
  total; a missing segment and a failed check; `:traces` without `--lens`
  under an applied filter; a pinned `:logs` page; the analyzer input of the
  pattern analysis for an entry that holds only segments and for a bound name
  with two spaces.
- Zero configuration still works
  ([ADR-0010](0010-curated-defaults-runtime-discovery.md)): no scope file
  means no scope, and every view opens as it does today.

## Open questions

This record stays Proposed, and its pull request open, until this list is
empty.

- **Does the owner of the `.dtui.yaml` segments feature agree with how an
  entry's segments sit beside it?** The proposal touches that feature in six
  places: the entry's segments appear in the picker as locked rows, `alt+s`
  does not pause them, a segment both files name is sent with the user's
  variables and with the entry's while `alt+s` pauses the user's, the limit of
  ten is enforced over the union by not applying the entry, the first query of
  a scoped view waits where the workspace's segments let it go out, and the
  pattern analysis says in its header that segments, the user's included, do
  not narrow it. dtctl's design document describes the same composition from
  its side. None of it is agreed. Also that owner's to answer: whether a
  pinned or drilled page keeps the entry's segments when it drops the entry's
  filter, whether the limit of ten counts segments the user has paused, what
  `v` does on a locked row, and whether variables an entry binds only in part
  are expanded as the workspace's are.
- **Does this much detail belong in `docs/adr/`?** The index asks for a
  record that can be read in two minutes. If a maintainer asks for a shorter
  one, the owed tests and the mechanics of the echo move to `docs/design/`
  and the record keeps the rules.
- **Which dtctl release does dtui build against?** Nothing here can be
  implemented before a release carries `pkg/reposcope`, and the package may
  change before then. The rules above that quote dtctl (the host form, the
  bound fields, the refusals, how the variable is read, the segment check) are
  read again against that release. Four things dtui would otherwise copy are
  asked of dtctl: an exported derivation of the host key, which today is an
  unexported function of dtctl's `cmd` package, an exported reading of
  `DTCTL_NO_REPO_SCOPE`, reasons that do not name a dtctl flag, and an
  exported spelling of a segment reference with its variables, which the echo
  prints and only dtctl's unexported flag parser defines. Until dtctl has the
  last one, dtui owns that spelling and a test pins it against the release.
