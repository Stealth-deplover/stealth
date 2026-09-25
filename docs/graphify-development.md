# Graphify for local development

Graphify is an optional local aid for code navigation, impact analysis, and post-change review. It is not part of Stealth's runtime or CI acceptance. Source code, PostgreSQL constraints, tests, OpenAPI, CI, and observed runtime behavior remain authoritative.

## Install for your user

Install Graphify as an isolated developer tool and add its Codex skill to your user configuration:

```bash
uv tool install graphifyy
graphify install --platform codex
```

The Codex integration should be user-level. Do not add Graphify to `go.mod`, `console/package.json`, production images, Compose services, or application APIs. Do not enable strict mode for this repository.

See the [Graphify project](https://github.com/Graphify-Labs/graphify) for current installation and command documentation.

## Keep the graph focused

For Apps work, start with a focused source set covering:

- `internal/appruntime/`, `internal/appstore/`, `internal/appbuilder/`, `internal/appbuildspec/`, and `internal/workloadspec/`;
- App-specific repository and HTTP API files, plus directly referenced shared types and worker/API entry points;
- `internal/ingress/` and `internal/platformhostname/`;
- `console/src/features/apps/` and the directly used generated API, cache, and realtime files.

Stage only the needed source files outside the checkout, preserving their repository-relative paths. Add files when queries reveal a direct dependency. Avoid unrelated large domains such as Agents, Messaging, and unrelated product UI. OpenAPI files, migrations, Compose, scripts, and docs still need direct review when a change touches them; they are not included merely because the code graph has related nodes.

Choose a staging directory outside the checkout:

```bash
export GRAPHIFY_STAGE="${XDG_CACHE_HOME:-$HOME/.cache}/stealth-graphify/apps-source"
mkdir -p "$GRAPHIFY_STAGE"
```

From the repository root, build a deterministic code graph into local tooling data:

```bash
graphify "$GRAPHIFY_STAGE" --code-only --no-cluster --out "$PWD"
```

Here `GRAPHIFY_STAGE` is the focused staging directory. To refresh after a change, first copy the changed source files into that staging tree, then run:

```bash
GRAPHIFY_OUT="$PWD/graphify-out" graphify update "$GRAPHIFY_STAGE" --no-cluster
```

The focused graph is incomplete by design. A missing node or path does not prove that no source relationship exists.

## Query, inspect, and audit

Use queries to locate likely files, then open those files directly:

```bash
graphify query "What updates App observed_generation?"
graphify path "HealthCheck" "App detail view"
graphify explain "AppRuntimeJob"
```

Graphify labels structural relationships as `EXTRACTED` and weaker relationships as `INFERRED` or ambiguous. `EXTRACTED` means the extractor found a source structure; it does not establish the product guarantee. Treat `INFERRED` links only as leads. Verify important conclusions against implementation, tests, migrations, security boundaries, and runtime behavior.

For a feature review:

1. Read the diff, migrations, and OpenAPI changes.
2. Inspect backend behavior and trust boundaries, then Console behavior and tests.
3. Run the prescribed tests and CI checks.
4. Update the focused graph and query the changed concepts and consumers.
5. Verify graph findings against source and check for missed callers or tests.

Never approve or merge a change solely because Graphify reports a path or a consistent graph.

## Generated output hygiene

`graphify-out/` contains machine-local derived data. Do not stage or commit generated graph JSON, HTML, reports, or caches by default, and do not change `.gitignore` solely to hide them. Check `git status --short` before committing Stealth changes. Graphify must remain outside product dependencies and production runtime.
