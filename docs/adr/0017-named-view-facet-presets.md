# ADR-0017: Named view facet presets are local narrowing, not tenant config

**Status:** Accepted · **Date:** 2026-11-18

## Context

dtui already lets a session narrow a catalog table (facets, `| search`,
timeframe). Repeating that narrowing from the CLI or another session needed a
name. ADR-0005 says dtui is a pure consumer of dtctl config and "ships no
config-write path"; UI-only state lives under `~/.local/state/dtui/`.
ADR-0011 forbids tenant mutations.

Two extra surfaces came with named presets: a file dtui itself writes
(`presets/<context>.yaml`) and a non-interactive `list-presets` / `delete-preset`
subcommand that must run before the TTY and AI-agent guards (those guards
exist so the *interactive* UI is not launched from a pipe or an agent).

## Decision

Named presets are **session narrowing**, the same class as workspace
`.dtui.yaml` segments and the navigation history file:

- A committed `.dtui.yaml` `presets:` map is project-local, read-only inside
  dtui. Edit the file in git to change it.
- `W` in the TUI writes only `~/.local/state/dtui/presets/<context>.yaml`
  (version + presets). That path is UI state, not dtctl config, and not a
  tenant write.
- Personal presets are **per dtctl context**, like navigation history:
  values such as namespaces and hosts are tenant-specific, so a preset saved
  on one context is not offered on another. The context is resolved as for
  a launch (`--context`, `DTCTL_CONTEXT`, workspace environment, current
  context). `:ctx` swaps the list, and `list-presets` / `delete-preset` act
  on the active context. The file name is the context name path-escaped
  (no separators, no leading dot). Committed `.dtui.yaml` presets stay
  shared: they belong to the project, which already pins its environment.
- `dtui --preset`, `:preset`, `dtui list-presets`, and `dtui delete-preset`
  select, list, or delete that local narrowing. They never POST to the
  tenant. `delete-preset` and `d` in the picker only touch the personal
  file.
- `list-presets` and `delete-preset` are cobra subcommands so they skip the
  interactive-only guards. The default `dtui` command stays TTY-only.

Zero configuration still works: no file means no presets, and every catalog
table is still reachable unnarrowed.

## Consequences

- ADR-0005's "no config-write path" continues to mean **dtctl config and
  credentials**. Personal presets are the same exception as `history.json`.
- ADR-0011 is intact: nothing about presets mutates the tenant.
- A broken personal file or `.dtui.yaml` must not be reported as "no
  presets" with exit 0; load warnings go to stderr.
- Saving from an entity-scoped table is refused (scope honesty, ADR-0007).
  `W` does not pin the session timeframe.
