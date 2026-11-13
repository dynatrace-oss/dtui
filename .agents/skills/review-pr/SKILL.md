---
name: review-pr
description: Review a dtui pull request as the guardian of the codebase and leave advisory comments on architecture, layering, reuse vs duplication, read-only safety, queries, tests, privacy, and docs/ADR/learnings drift. Use when asked to review a PR, a branch, or your own change before opening it. Run in a fresh session that did not write the change.
---

# Review a PR as the codebase's guardian

You protect dtui's architecture and its written knowledge from this change.
You do not gate it: your output is comments and questions. Never approve,
request changes, push commits or block a merge — the author and maintainers
decide. A good review asks "what about…?" where the change and the
codebase's rules meet.

## 1. Load context, cheapest first

```bash
gh pr view <n> --repo dynatrace-oss/dtui --json title,body,files,commits,headRefOid
gh pr diff <n> --repo dynatrace-oss/dtui      # or: git diff upstream/main...HEAD
gh pr checks <n> --repo dynatrace-oss/dtui
```

- Read the diff before the description. Judge the code, then check the
  description matches it.
- Read `AGENTS.md` (constraints, reuse table, write-back table). Open other
  docs only for the areas the diff touches, via its routing table.
- Do not redo what CI does (lint, format, tests, invariants). Read the
  failures, if any, and move on.

## 2. Ask these questions

Skip any that do not apply. Each finding must point at a line or file.

**Architecture and layering**

- Does it follow the existing pattern for its kind of change (a catalog
  `Spec`, a `Source`, an overlay, a `viewModel`), or invent a parallel one?
- A bespoke screen, new package or new global without an ADR?
- Imports pass CI; does the *intent* hold? Look for tenant access smuggled
  in through a func field, state or rendering pushed into `catalog`,
  workspace keys that carry credentials or actions.

**Read-only safety (ADR-0011)**

- Any call that could change a tenant: POST/PUT/PATCH/DELETE, "execute",
  get-or-create endpoints (the Live Debugger workspace lookup is one), any
  dtctl handler method beyond get/list.
- Tenant-supplied text rendered without `SafeText`?

**Reuse vs duplication**

- For each new function of more than ~10 lines, search for an existing one
  (`rg -n 'func .*<keyword>'`, the AGENTS.md reuse table). Near-copies →
  suggest extending the shared helper.
- A general helper placed in a feature file → suggest moving it to a
  shared file and adding it to the reuse table.
- A changed shared helper → are all its callers still right? Name them.

**Queries (if DQL changed)**

- Does the PR say the query was validated live and returned rows?
- Scoped (no broad unscoped signal query)? Within the query budget
  (ADR-0013)? Does the view claim only the scope it applies (ADR-0007)?
- Known traps from `docs/dev/learnings.md` (uid vs string, field names)?
- A new fact discovered → is there a learnings entry?

**Tests**

- A bug fix with a test that fails without the fix?
- New behaviour covered by model and catalog tests?
- Golden files changed only where the layout change is intended, and only
  for main screens? No HTTP, TTY or sleeps; synthetic identifiers only.

**Docs and memory drift**

- Does the diff make any doc statement false? Grep the old names:
  `rg -n '<renamed identifier|file|key>' AGENTS.md CONTRIBUTING.md README.md docs .agents`.
- New or changed key → `?` help (`internal/tui/help.go`) and README "Keys"?
- New view → README "What you can browse" and `docs/design/tui.md`?
- A decision whose reversal would ripple beyond one view → ADR needed?
  Contradicts an ADR → it must supersede it explicitly.
- New shared helper or hard rule → AGENTS.md tables (and a test for a rule)?
  A shortcut left behind → `docs/dev/tech-debt.md`? Multi-PR work → a plan
  in `docs/plans/`?

**Scope and privacy**

- Passes the six vision gates; touches no anti-goal? Unrelated changes
  bundled in?
- Tenant names, URLs, IDs, hostnames, people or ticket keys anywhere,
  including goldens, fixtures and the PR body.

## 3. Write the comments

- One comment per finding, anchored to `path:line`. Each says what you see,
  why it matters (cite the rule, ADR or doc), and a concrete question or
  suggestion.
- Prefix a label: `invariant` (breaks a hard constraint), `concern`,
  `question`, `docs-drift`, `reuse`, `nit` (at most three).
- Say nothing about what linters and CI enforce, about taste, or to praise.
  If unsure, ask instead of asserting. Do not dump the checklist — a
  category with no finding is not mentioned.
- Finish with a summary: the top findings, then one line
  `Docs/ADR/learnings: updated | not needed | missing: <what>`.

## 4. Post as a comment-only review

Write the review to `review.json` and post it with event `COMMENT` (never
`APPROVE` or `REQUEST_CHANGES`):

```json
{"commit_id": "<headRefOid>", "event": "COMMENT", "body": "<summary>",
 "comments": [{"path": "internal/tui/x.go", "line": 42, "side": "RIGHT", "body": "question: …"}]}
```

```bash
gh api repos/dynatrace-oss/dtui/pulls/<n>/reviews --input review.json
```

If you cannot post, return the same content as Markdown to whoever asked.
