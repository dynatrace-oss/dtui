# Plans

A plan is for work that spans more than one PR, or needs a design decision
agreed before code. Single-PR work does not need one: the PR description is
the plan.

- One file per plan: `docs/plans/active/<slug>.md`. Move it to
  `docs/plans/completed/` in the PR that finishes the work.
- Keep it to about a page. Describe behaviour and interfaces, not code;
  the code is written in the PRs.
- Update it in every PR that advances it, so the next agent can pick the
  work up from the file alone.

## Template

```markdown
# <Title>

Status: active | completed · Issue: #<n>

## Goal
What the user can do when this is done. Which vision gates it touches.

## Non-goals
What this deliberately does not do.

## Approach
The shape of the change: specs, sources, interfaces, ADRs needed.

## Steps
- [ ] <step> (PR #<n>)

## Decisions
- <date>: <decision> — <why>

## Open questions
- <question>
```
