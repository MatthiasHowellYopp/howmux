# AGENTS.md

Repo conventions for agents working on howmux. The howmux pipeline agents read
this file; the agents themselves live in ai-resources (see README, "Agent
Configuration").

## Build, test, lint

Use the Task targets, not the underlying tools (see
`.kiro/build-instructions.md`). Run `task --list` for all targets.

| Operation | Command |
|-----------|---------|
| Build | `task build` |
| Test | `task test` (`go test -v -race ./...`) |
| Lint | `task lint` (`go vet ./...`) |
| Format check | `task fmt:check` |
| Template sync check | `task sync:check` |

Running a single test with `go test -run <name> ./<pkg>` is fine; CI runs the
Task targets.

## Template synchronization (required)

`howmux init` and `howmux update` install files embedded from
`cmd/howmux/templates/`. howmux runs on itself, so the live files and their
embedded copies must match. Sync is one-way, live → template. CI runs
`task sync:check`.

| Live path | Template path |
|-----------|---------------|
| `.howmux/scripts/*.sh` | `cmd/howmux/templates/howmux/scripts/` |
| `.howmux/themes/*.yaml` | `cmd/howmux/templates/howmux/themes/` |
| `.howmux/evals/fixtures/*` | `cmd/howmux/templates/howmux/evals/fixtures/` |
| `.howmux/evals/rubrics/*` | `cmd/howmux/templates/howmux/evals/rubrics/` |
| `.howmux/evals/cases/**/*` | `cmd/howmux/templates/howmux/evals/cases/` |

Not synced: `.howmux/evals/results/`, `.howmux/evals/baseline.json`, `tmp/`
directories, and `.howmux/config.yaml` (user settings, never overwritten).

If a task creates, edits, renames, or moves a file under a live path above,
copy it to the template path in the same change, then run `task sync:check`.
New files count, not just edits. A task isn't complete while `task sync:check`
fails.

```bash
cp .howmux/scripts/*.sh cmd/howmux/templates/howmux/scripts/
cp .howmux/themes/*.yaml cmd/howmux/templates/howmux/themes/
cp .howmux/evals/fixtures/* cmd/howmux/templates/howmux/evals/fixtures/
cp .howmux/evals/rubrics/* cmd/howmux/templates/howmux/evals/rubrics/
mkdir -p cmd/howmux/templates/howmux/evals/cases/
cp -r .howmux/evals/cases/* cmd/howmux/templates/howmux/evals/cases/
task sync:check
```

## Agents are not in this repo

Don't add agent configs, agent prompts, or `*-conventions` skills under
`.kiro/` or `cmd/howmux/templates/`. They live in ai-resources
(`agents/src/`, `prompts/`, `skills.yaml`). An issue that changes agent
behavior needs a change in ai-resources, not here.
