# ADR-0018: Launch flags are a handoff contract

**Status:** Accepted; "never drive dtui" partially superseded by
[ADR-0019](0019-agents-may-navigate-a-listening-tui.md) · **Date:** 2026-11-18

## Context

Agents investigate with dtctl (the JSON surface, ADR-0012) and then want the
user to see the evidence. dtui is interactive-only and refuses to start in a
detected agent session (ADR-0006, vision anti-goal "not an agent surface").
Before this decision the CLI could only name a view, a preset and facets, so
a handoff was prose ("press `:`, type …, set the timeframe to …").

## Decision

The root command's launch flags are a **stable contract for a command a human
runs**: `<view> [arg]`, `--timeframe`, `--from`/`--to`, `--facet`, `--search`,
`--preset`, `--lens`, `query <dql>` / `--dql` / `-f`, `--context`.

- Agents *emit* these commands; they never drive dtui. The TTY and agent
  guards are unchanged.
- A positional argument means what it means after the view in the command
  bar; there is no second grammar.
- A flag that cannot apply is an error, never silently dropped (ADR-0007):
  facets/search on a non-DQL view, a window with `--dql` (the query owns its
  `from:`), an absolute time without a zone.
- Flags are additive and backward compatible. Renaming or changing the
  meaning of one is a breaking change for every command already handed out.

## Consequences

- Nothing new reaches the tenant: the launched page runs the same single
  query a user would (ADR-0011, ADR-0013).
- Agents still cannot confirm a command opens on rows; a non-TTY check that
  prints the DQL would close that gap (#65 follow-up).
- New page state worth handing over (entity scope, lens) should arrive as a
  flag here rather than an opaque encoded page.
