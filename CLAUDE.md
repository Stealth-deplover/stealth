# Claude Code Guidance

Before substantial work, read the root [`AGENTS.md`](AGENTS.md). Also read any
nested `AGENTS.md` that applies to the files being changed, especially
[`console/AGENTS.md`](console/AGENTS.md) for Console work.

## Available Project Skills

These skills are committed project dependencies and are available locally to
Codex and Claude Code:

- `vercel-react-best-practices`
- `web-design-guidelines`
- `diagnosing-bugs`
- `improve-codebase-architecture`

Load a skill only when it is relevant to the task. Follow repository-specific
instructions, established Stealth architecture, API compatibility, and
security boundaries over generic skill guidance. Preserve the existing product
scope and avoid unrelated refactors.

Run the relevant tests and checks before declaring work complete. Treat changes
to `.agents/skills/`, `.claude/skills/`, and `skills-lock.json` like dependency
changes: inspect their instructions, scripts, hooks, shell commands, and
network access before accepting updates.
