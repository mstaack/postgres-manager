# Handoff — Go migration (`pgm`)

Status as of 2026-11-06. Branch `go-migration` (worktree `.worktrees/go-migration` in the main checkout). Nothing pushed.

## Where things are
- Master plan (all phases, decisions): `docs/superpowers/plans/2026-11-06-go-migration.md`
- Phase 0 detailed plan (6 tasks): `docs/superpowers/plans/2026-11-06-go-phase-0-skeleton.md`
- Execution method chosen by the user: **subagent-driven** (superpowers:subagent-driven-development): fresh implementer per task → task review (spec + quality) → fix loop → next task; final whole-branch review on the most capable model. Use cheaper models where the brief is complete (user: "use simpler model if needed").
- After Phase 0: continue with **all phases** (1–6) without stopping; write each phase's detailed plan first (superpowers:writing-plans), same format as the Phase 0 plan.

## Progress (Phase 0)
- Task 1 (module, `cmd/pgm`, cobra root, `--version`, `go-*` make targets): **complete**, review clean. Commits fa137eb..bb7521d.
- Task 2 (`internal/sys`): **in progress** — only the failing tests were written (commit "WIP sys: Task 2 tests"). `go test ./...` does not compile until `internal/sys` is implemented. Resume at Task 2 Step 3 (implement), then review.
- Tasks 3–6: not started.

## User decisions (binding)
- Go, binary `pgm` (+ `postgres-manager` symlink), systemd timers, Forge auto-detect kept, extra features: major upgrades, second repository, single-DB restore. R2 EU is the default repository; local path and other S3 as alternatives; backups/restores picked from lists.
- **No bash needed at all** (2026-11-06): no bash/Go coexistence, no `PGM_IMPL` switch, no legacy `backup.conf` import, no cron→timer migration, no bash bridge for self-update. `src/` stays only as read-only reference until its feature is ported, then gets deleted. The master plan's Phase 2.2 and 6.4/6.5 are dropped accordingly.

## Binding constraints and rulings for implementers
(copied from the git-ignored SDD workspace `.superpowers/sdd/2026-11-06-go-phase-0-skeleton/`; recreate it with the skill's `scripts/sdd-workspace` and paste this back as `constraints.md` / `progress.md`)

## Global Constraints

- Module path `github.com/mstaack/postgres-manager`; binary name `pgm`; build `CGO_ENABLED=0 GOOS=linux`, amd64 + arm64, `-trimpath`, version injected with `-ldflags "-s -w -X main.version=$(cat VERSION)"`. `VERSION` file content: `1.0.0-dev`.
- No `os/exec` outside `internal/sys`. Secrets are passed only through `Cmd.Stdin`.
- Normal output and prompts go to stdout; errors go to stderr as `✗ <message>`. Exit codes: 0 ok, 1 error, 130 cancelled.
- No colour when stdout is not a TTY or `NO_COLOR` is set.
- Everything that touches the cluster runs as `postgres` via `runuser -u postgres --`.
- The bash tool keeps working: don't modify `src/`, `postgres-manager` or the existing bash Makefile targets (`build`, `dev`, `lint`, `test`). Go targets are added beside them with a `go-` prefix.
- Test containers carry `--label postgres-manager-test=1`; never kill unlabelled containers.
- Commit messages: one short technical summary line, no attribution lines.


## Controller rulings (binding)
- The bash tool is being dropped (user decision): no bash/Go coexistence. Ignore "The bash tool keeps working" above for anything except: do not delete src/ yet (it's the reference implementation). e2e runs the Go binary only (no PGM_IMPL switch); Go Makefile targets may replace the bash ones in Task 6.
- VERSION content is exactly `1.0.0-dev`.

## SDD ledger

# SDD ledger — plan: docs/superpowers/plans/2026-11-06-go-phase-0-skeleton.md
Spec: docs/superpowers/plans/2026-11-06-go-migration.md (master plan) + AGENTS.md + src/ (bash reference)
Branch: go-migration (worktree .worktrees/go-migration), started from 014c76e

## Pre-flight scan
| Tasks | Produces → Consumes | Finding |
|---|---|---|
| T1↔T3 | T1 creates internal/ui/ui.go with ErrCancelled; T3 extends the same file | consistent |
| T1↔T6 | T1 Makefile go-test reads test/go-suites; T6 creates it | ok: go-test unused before T6 |
| T2↔T4 | sys.Cmd/Runner/FakeRunner/Key/WriteFileAtomic → pg | consistent signatures |
| T3↔T4 | ui.UI/NewTest/Choose/Warn/Confirm → SelectCluster, CheckOverrides | consistent |
| T2..T5↔T6 | sys, ui, pg, format, apps → status/cli | consistent |
| T1 self | test newRoot vs code newRoot/Execute | ok |
| T2 self | lock test in-process relies on flock per open file description | ok (flock semantics) |
| T3 self | tests line mode only; TTY path manual | ok |
| T4 self | WriteSnippet test needs etcRoot + chown skip on macOS | specified |
| T5 self | ok |
| T6 self | TestStatusClusterDown on macOS: statfs of /var/lib/postgresql/... fails | gap → Ruling 1 |
| T6 self | 10-cli e2e assertions vs Go command tree/messages | consistent (setup' first, Run as root, VERSION) |

Ruling 1: Disk row (statfs/mounts) failure renders "?" like failed queries — the plan says failing queries show "?", and Disk is the same class — if wrong, only cosmetic.
Ruling 2: Git setup — committed the bash WIP on main (1dfea19) + master plan (014c76e), Go work on branch go-migration in a worktree — the user left it to me ("just build it nicely") — if wrong, the commits can be moved/squashed; nothing is pushed.

## Tasks
Ruling 3: User (2026-11-06): "continue with all phases after phase 0, wont need bash at all, use simpler model if needed" → no bash coexistence/compat: drop PGM_IMPL dual mode, bash CI jobs, legacy backup.conf import, cron→timer migration and the bash bridge (master plan 6.4/6.5/2.2). src/ stays only as read-only reference until its feature is ported; removed at the end. Version 1.0.0-dev. — cost if wrong: existing bash installs need a manual reinstall + re-setup.
Ruling 4: golangci-lint pin v2.1.6 → v2.4.0 (first v2 release supporting Go 1.25; an older build refuses a go 1.25 module) — reviewer ⚠️ item — if wrong, bump to the next tag that runs.
Task 1: minor (deferred): make help regex skips hyphenated targets (fixed by Task 6 Makefile rewrite)
Task 1: fix round 1/5 (2 addressed, 0 open; commits 895ae69..bb7521d)
Task 1: complete (commits fa137eb..bb7521d, review clean)
