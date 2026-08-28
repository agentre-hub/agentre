# AGENTS.md

This file provides unified guidance for all AI coding agents (Claude Code, Codex, etc.) working in this repository.

## Repository Facts

- Agentre is a Wails v2 desktop app: Go 1.26 backend + React 19 / TypeScript frontend.
- Main tech stack: Go 1.26, Wails v2, React 19, TypeScript, Vite, Tailwind CSS v4, pnpm 10.33.
- The Go module path is `github.com/agentre-hub/agentre`.
- Frontend-backend IPC only goes through the Wails bindings in `internal/app`; the generated bindings live in `frontend/wailsjs`; **do not add HTTP-style app APIs**.

This repository produces three binaries:

- **`agentre`** (root `main.go`) — the desktop app, **GUI-only**. It no longer routes any CLI subcommand; the hook shim moved out (see `agrctl`).
- **`agentred`** (`cmd/agentred/`) — a headless daemon that executes claude-code / codex subprocesses on behalf of a paired desktop over binary Protobuf RPC on WebSocket. The daemon-side handlers live in `internal/daemon/`.
- **`agrctl`** (`cmd/agrctl/`, `make agrctl`) — a small companion CLI carrying the `claudecode` hook shim (`internal/cli/claudecodecmd`) and the `ctl` control CLI (`internal/cli/ctlcmd`). The app installs it under `<AppDataDir>/bin` and points the Claude Code hooks at it, so a hook subprocess never boots the GUI binary.

## High-Priority Constraints (mandatory, non-negotiable)

The following are hard rules. If the current task conflicts with them, **stop and ask the user first** — do not work around them on your own.

1. **Strict TDD / BDD: Red → Green → Refactor, no exceptions.**
   - For a new feature, first write a BDD-style behavior spec (`Given … When … Then …` or goconvey `Convey("when X, then Y")`) covering the happy path plus at least one boundary/error case, **then write the implementation**.
   - Do not write implementation code without a failing test. See [docs/develop.md](docs/develop.md) for details.
2. **Verify the bug exists before fixing it.**
   - Write a regression test that reproduces the failure, **run it and watch it fail** (and fail for the right reason), then start patching.
   - If the bug genuinely cannot be reproduced in a test, **tell the user explicitly**, and then discuss the patch approach. Do not silently "this is probably how to fix it" and change code.
3. **Prefer refactoring over patching — fix the root cause, don't mask it.**
   - Fix the bad value the producer emits, instead of adding an `if x == nil` fallback guard at every consumer; don't repeatedly normalize the same field at multiple call sites — normalize once at the boundary.
   - A comment like `// workaround because X returns Y` is a smell; the code underneath most likely needs to change. Refactor bad structure away when you can rather than piling on patches — but keep the refactor **within the scope of the current task** and don't let it spill over.
4. **Do not modify files unrelated to the current task.**
   - The diff should only touch the producer + its tests, plus at most one obvious in-scope drift.
   - **No** drive-by refactor / rename sweep / formatter pass / dead-code cleanup / import reordering / unrelated test churn — they bury the real change and break `git bisect`.
   - When you see unrelated dirty data, flag it to the user and ask, **do not fix it in passing**.
5. **New visible frontend UI copy must go through i18n.**
   - New UI text uses `react-i18next`'s `t(...)`, and updates both `frontend/src/i18n/locales/zh-CN/` and `frontend/src/i18n/locales/en/` — one tree split into domain modules (`chat.json` / `settings.json` / …) merged by each language's `index.ts`; the module name is not part of the key.
   - Do not add hardcoded Chinese; ESLint, via `eslint-plugin-i18next`'s `i18next/no-literal-string`, blocks hardcoded Chinese UI copy in JSX text and visible attributes.
   - Static `t("...")` keys and locale coverage are validated by `frontend/src/__tests__/i18n.test.ts`; run the relevant tests when you change copy.
   - Do not translate dynamic output such as agent / user / terminal / code / markdown; it naturally never enters `t(...)`, and using a global text-rewrite fallback is forbidden.
   - All static UI copy is explicitly `t(...)`. See [docs/frontend.md](docs/frontend.md) for details.
6. **Cross-host frontend concepts have one implementation.** Before adding or changing UI that also exists in `agentre-server`, inspect `frontend/packages/agentre-ui`. Shared components, view contracts, pure presentation helpers, copy and tokens belong there; desktop state, Wails, navigation and platform behavior stay in this host and connect through props or ports. Do not keep a desktop copy and a server copy in sync by hand. The extraction and cross-repository delivery order is mandatory; see [docs/frontend.md](docs/frontend.md#shared-ui-package-agentre-hubagentre-ui).

## Engineering conventions and task routing

For every code change, read [docs/develop.md](docs/develop.md) for SOLID, cohesion/coupling, Red → Green → Refactor, Fix Discipline, persistent-data handling, and delivery. Read the other owner documents required by the task; they are an index, not a requirement to read every document for every change:

- [docs/architecture.md](docs/architecture.md) — repository layout, cago layering conventions (entity / repo / service / wails binding), shared frontend host-seam patterns, remote-execution architecture, storage paths and environment overrides, database/migration flow, and generated files.
- [docs/develop.md](docs/develop.md) — Red→Green→Refactor, SOLID, high cohesion / low coupling, Fix Discipline, code style, the **enforced-rules table** (every convention that has a real mechanical check, with its guard test and exemption), the persistent-data process, commit / PR flow, and the CI gate.
- [docs/testing.md](docs/testing.md) — how a test is **designed**: the applicability gate, choosing a boundary, covering the behavior space, the test stack (`testutils.Database(t)` + sqlmock + mockgen + goconvey + vitest), guard tests, what not to write, cleanup boundaries, and `//nolint` exceptions.
- [docs/observability.md](docs/observability.md) — the logging convention: the single `cago/pkg/logger` entry point, level selection, the five mandatory instrumentation points, the `package.Method:` prefix with **camelCase** `zap` fields, and the never-log red line. (No metrics/tracing in this project.)
- [docs/frontend.md](docs/frontend.md) — the mandatory shadcn-from-`@agentre-hub/agentre-ui` convention, i18n, working inside the shared `@agentre-hub/agentre-ui` package (entry points, what belongs in it, `useUiTranslation`, peer-vs-dep, its guards), pnpm, formatting / lint (`make lint` / `gofmt` / `goimports`), and the module path.
- [docs/design.md](docs/design.md) — the frontend **design-system reference**: color tokens (full light/dark values), the 16-color agent palette + run-status system, theming, the desktop window shell, the component palette, motion, state patterns, accessibility, and the new-page recipe. The visual-language companion to `frontend.md` (which owns the enforced shadcn / i18n / lint rules).
- [docs/debugging.md](docs/debugging.md) — sqlite3 / jq / log-filtering commands, the table-to-feature mapping, the command checklist for reproducing production bugs, and common pitfalls (on macOS the `Application Support` path must be quoted).
- [docs/agent-backend.md](docs/agent-backend.md) — the full path for wiring up a new AI agent backend (entity / migration / runtime / translator / capability / daemon import / frontend gating), including the TDD test checklist and common anti-patterns.
- [docs/session-lifecycle.md](docs/session-lifecycle.md) — creation, reuse, sidebar visibility, and remote-execution ownership for `chat_sessions`.
- [e2e/README.md](e2e/README.md) — the unified E2E / verification harness: `make e2e` runs the independent hermetic Wails app through one runner/config and the desktop, sync-client, and remote-peer smoke boundaries; `make verify-up` launches only the isolated formal desktop main for one-action-at-a-time `drive.mjs` verification; includes storage/process guards, protocol fakes, SQLite oracles, and sanitized per-run artifacts.
- [docs/verification.md](docs/verification.md) — the verification **route** and what a run has to leave behind: when driving the real app is warranted at all, start → drive → record → stop, the one-scenario-one-directory evidence layout under `e2e/scratch/<scenario>/`, creating `report.md` **before** the run, reporting honestly (never describing red as green), and the one-place-only verdict table for spec acceptance. The template it copies is [docs/references/verification-report-template.md](docs/references/verification-report-template.md).
- [docs/specs/](docs/specs/) — dated design/decision snapshots; use them for the scoped design history, not as the source of current engineering conventions or behavior.
- [docs/documentation.md](docs/documentation.md) — required before changing any contributor doc (`AGENTS.md` / `CLAUDE.md` / `docs/*`): Git-aware fact-checking, doc ownership, link checking, and stale-content removal.
> See the cago skill (`/cago`) for details — complete controller / service / repo / cron / queue unit-test examples.

## Task-specific hard boundaries

- **Repository unit tests always use `testutils.Database(t)` + sqlmock**; spinning up a real SQLite is forbidden (`internal/bootstrap/cago_test.go` and `internal/daemon/daemon_test.go` are the only exceptions). See [docs/testing.md](docs/testing.md).
- **New or modified service unit tests** generate a repo mock via `mockgen`, inject it via `RegisterXxx`, and **do not connect to a DB**. Legacy sqlmock-backed service tests are migration debt; do not copy or expand them.
- **Append new migrations to the end of `migrationList()`**; modifying an existing migration is forbidden; prefer native SQL for DDL, avoid relying on `AutoMigrate`. Migrations carry **no unit tests of their own** — do not add a `migrations/*_test.go`. `internal/bootstrap/cago_test.go` only proves the chain runs clean on a *fresh* database; backfill and key-change semantics are verified by hand against a database holding real rows, per [docs/develop.md](docs/develop.md) "When Touching Persistent Data" step 4.

## Common Commands

```bash
make install-deps     # pnpm install in frontend/
make dev              # wails dev — hot reload
make build            # wails build with version/commit ldflags (current platform)
make run              # build and launch production app
make install          # build + install app bundle (macOS: /Applications/Agentre.app)
make generate         # wails generate module — refresh frontend/wailsjs/ bindings
make test             # backend Go tests + frontend Vitest (runs `generate` first)
make test-backend     # Go tests excluding /frontend/
make test-frontend    # wails generate + frontend Vitest
make test-cover       # coverage.out + coverage.html
make lint / lint-fix  # golangci-lint + frontend ESLint (runs `generate` first)
make check            # lint + test
make mock             # go generate ./... (go.uber.org/mock)
make clean            # rm build/bin frontend/dist coverage.*
make e2e              # unified isolated desktop/sync-client/remote-peer smoke suite
make verify-up        # launch isolated formal desktop for drive.mjs verification
make verify-status    # inspect the live formal verification target
make verify-down      # stop it; add VERIFY_FLAGS=--wipe to remove isolated state

# agrctl companion CLI (claudecode hook shim + ctl control CLI)
make agrctl                  # build → build/bin/agrctl (the app installs it into <AppDataDir>/bin)

# agentred daemon (remote execution box)
make agentred                # build local-platform binary → build/bin/agentred
make agentred-linux          # cross-build linux/amd64 (override via AGENTRED_GOOS/ARCH)
make agentred-deploy         # build linux + opsctl-cp + install (AGENTRED_TARGET= host)

# Focused tests
go test -race -run TestName ./internal/service/chat_svc/...
go test -race ./internal/repository/llm_provider_repo -run TestName
go test -race ./pkg/codex -run TestName
cd frontend && pnpm test -- path/to/file.test.tsx
cd frontend && pnpm exec tsc -b --noEmit     # typecheck — vitest does NOT check types
cd frontend && pnpm install                  # pnpm is source of truth, not npm
```

> `go test ./...` in this repository will scan a Go package under `frontend/node_modules`; by default use `make test-backend` (which explicitly excludes `/frontend/`).
