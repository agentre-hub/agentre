# Architecture

Agentre's code organization, layering conventions, remote execution architecture, and persistence layout. Read this before writing new code.

## Project layout

```text
main.go                        (wails bootstrap; GUI-only — no CLI subcommands)
cmd/agentred/                  (headless daemon binary — main.go / root.go / run.go / pair.go / status.go / client.go / claudecode.go / llm.go)
cmd/agrctl/                    (companion CLI binary: `claudecode` hook shim + `ctl` control CLI)
internal/
  app/                         (Wails bindings — one file per domain: app.go / agent.go / chat.go / project.go / …,
                                methods only do parse → svc.Xxx().Method(ctx, …) → return)
  bootstrap/                   (startup order: dataDir → cago memory config → logger → SQLite → migrations)
  cli/{claudecodecmd,ctlcmd}/  (subcommand implementations, compiled into the agrctl binary)
  daemon/                      (agentred-side daemon: client / handlers / migrations / notifier / pairing / remotefs / repository / rpc / sessions / state / workspacefs)
  service/<domain>_svc/        (business logic; interface + singleton accessor + private implementation)
  repository/<domain>_repo/    (data access; interface + Register/accessor, uniformly going through db.Ctx(ctx))
    mock_<domain>_repo/        (mockgen output, injected into service unit tests)
  model/entity/<domain>_entity/(rich domain entity; GORM tag + business methods)
  pkg/                         (cross-cutting internal packages: agentprovider / agentruntime / agentskill / agenttool /
                                agrctlinstall / ccoauth / claudecodehook / clienv / cliprober / cliprocess / code (i18n error
                                codes) / ctlendpoint / diff / hookexec / httpgateway / jsonrpc / keychain / llmcatalog /
                                llmurl / openclawgateway / paths / procattr / pty / remotefs / sysnotify / workspacefs)
  buildinfo/                   (CommitID ldflag target)
migrations/                    (gormigrate sequential migrations, filename prefix YYYYMMDDNNNN)
pkg/                           (externally reusable packages: claudecode / codex / piagent —— independently maintained CLI subprocess wrappers;
                                agentred/protocol —— shared agentred wire protocol)
frontend/                      (React 19 + TS + Vite + Tailwind; wailsjs/ is wails-generated, gitignored)
  packages/agentre-ui/         (@agentre-ai/agentre-ui —— the shared frontend layer, also consumed by agentre-server;
                                design tokens + transcript renderer + data contract. See below and frontend.md)
  packages/agentre-wire/       (@agentre-ai/agentre-wire —— the wire protocol's TS side: codec + golden samples, both
                                generated from internal/pkg/agentruntime/runtimes/remote/wire. See frontend.md)
e2e/                           (independent hermetic Wails app/composition + one Playwright runner/config;
                                formal agentre/agentred dependency graphs do not import it)
```

The `App` struct lives in `internal/app/app.go` (lifecycle + common methods), with domain methods spread across sibling files (`agent.go`, `chat.go`, …). **Keep these bindings thin — logic inside `App` is unreachable from `go test`; always put business logic in `service/`.**

## The shared frontend package (`@agentre-ai/agentre-ui`)

`frontend/packages/agentre-ui` holds the frontend layer that the desktop app and `agentre-server` both render: design tokens, the transcript renderer (markdown / thinking / code / canonical tool cards / activity blocks), the row model, and the data contract they agree on. The desktop consumes it through a Vite alias to the package **source**; `agentre-server` installs it as a dependency and consumes the built `dist/`.

**It is a leaf layer, the frontend counterpart of `internal/pkg/`.** Every layer may import it; it imports no host code — no `@/` alias, no `wailsjs/`, no zustand store. That direction is not a convention held by review: `packages/agentre-ui/src/boundary.test.ts` scans the package sources as text and fails on any of them, because a host coupling still *resolves* on the desktop (the alias is there) and only breaks when the server builds.

Anything the package needs from its host arrives through one of two seams:

- **`TranscriptPorts`** — actions with a side effect (answer a permission, resolve a plan action, open a path). A plain object of functions; the desktop wires it to Wails in `src/components/agentre/transcript-ports-desktop.ts`, the server wires it to relay RPC. Missing **optional** ports mean "this host lacks the capability", and the component hides the affordance rather than rendering a dead control.
- **`TranscriptLiveState`** — reactive reads of the host's in-flight stream, plus the optimistic write that pairs with them. Separate from ports because `useIsStreamActive` must be a **hook**: hanging it off the ports constant would return the value at call time with nothing to re-render when the stream ends. Unlike ports it has a working default, since "this host has no live streams" is a legitimate shape rather than a wiring gap.

Consumption rules, the entry points, and the i18n namespace are [`frontend.md`](frontend.md)'s.

`frontend/packages/agentre-wire` is the second shared package and deliberately a separate one: it carries the **TypeScript side of the wire protocol** — the frame types, their codec, and the golden samples — with **zero runtime dependencies**, so it stays out of `agentre-ui`, whose React / tiptap peer dependencies have nothing to do with decoding a JSON-RPC frame. Almost all of it is **generated from `wire.go`**, which is what makes `wire.go` the protocol's single source of truth instead of one of two hand-maintained copies. Its rules live in [`frontend.md`](frontend.md#shared-wire-package-agentre-aiagentre-wire).

## Remote execution (remote chat)

The desktop app can dispatch a single chat to an `agentred` daemon on the LAN for execution:

```
UI
  → internal/app Wails binding
  → chat_svc
  → internal/daemon/client (JSON-RPC client)
  → agentred (internal/daemon/{rpc,handlers,repository})
  → claude-code / codex subprocess
```

- Tool approval / ask-user-question are still rendered by the desktop UI.
- A dropped daemon connection no longer aborts the chat: the desktop client backs off and reconnects, then replays missed notifications from a cursor; an error is only injected into the turn once reconnection is abandoned (`internal/pkg/agentruntime/runtimes/remote/reconnect.go`). See [`session-lifecycle.md`](session-lifecycle.md#remote-execution) for session ownership and cursor semantics.
- `agentred` keeps its own SQLite store (`agentred.db` in its own data directory, schema in `internal/daemon/migrations`, access in `internal/daemon/repository`): every notification is journaled with a gap-free monotonic `seq` before being pushed, so a dead connection suspends pushing without losing the record. The journal is kept forever — `agentred` does not reclaim any of it, so its local database grows without bound and cleanup is left to a future pass; the database's path and size are reported by `agentred status` and `/local/status`; the LAN `health.ping` reports only the size, since the absolute path usually carries the host's OS user name.
- Reopening the desktop app catches up on what ran while it was closed: `chat_svc.CatchUpRemoteSessions` reads the execution-position columns on `chat_sessions`, asks each paired daemon which of those sessions it is still running, and replays the journal into synthesized turns. Details — push-target ownership, cursor validity and the startup cleanup split — are in [`session-lifecycle.md`](session-lifecycle.md#remote-execution).
- pairing / device status go through `internal/pkg/remotefs` + `remote_device_svc`.
- A claimed daemon exposes authenticated `engine.test`, `engine.discover`, and `engine.scan` RPCs for account engine settings. They read provider credentials only from daemon `state.json`; their receipts contain neither API keys nor CLI paths. `engine.scan` maps a locally found CLI to `recognized` and an absent CLI to `unchecked`. An unclaimed daemon rejects all three methods, while its existing paired-desktop `llm.*` RPCs remain available.
- A logged-in `agentred` is a narrow engine-snapshot consumer, not a `sync_objects` client. Login success, every successful relay connection/reconnection, and account-channel `sync_version` signals pull device-JWT `GET /v1/engine/snapshot`. Each successful pull atomically replaces `state.json`'s complete `llmProviders` map (so absent keys are deleted); a failed pull retains the previous map and is isolated from running rounds. Per-device CLI overlays remain in daemon memory and are applied only while resolving a backend for execution—absolute paths never enter `state.json`. Before the first successful account snapshot, paired-desktop `llm.upsert` and its supplied execution path keep their existing behavior.

## Layering conventions (cago framework style)

- **Entity (rich model)** — validation around a single entity (`Check(ctx)`), state checks (`IsActive()`), and field serialization (`GetXxx/SetXxx`) methods all live on the entity. The service only coordinates across entities and orchestrates external dependencies. **Do not cram all the rules into the service.**
- **Repository** — consumer-defined pattern: `type XxxRepo interface { ... }` + `func Xxx() XxxRepo` + `RegisterXxx(impl)`. Queries uniformly use `db.Ctx(ctx).…`. Transactions:

  ```go
  db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
      ctx = db.WithContextDB(ctx, tx)
      // … all repo calls inside the transaction transparently go through tx
  })
  ```

- **Service** — interface + singleton + private implementation; the service depends only on the repository interface (dependency inversion), which makes mockgen unit testing easy. Use `gogo.Go(func() error { … })` for background tasks, and **do not pass the request ctx into the goroutine.**
- **Error / i18n** — error codes are defined in `internal/pkg/code/code.go`, allocated in domain-specific `iota + base` ranges beginning at 10000 (`OperationFailed = iota + 10000`, `LLMProviderNotFound = iota + 11000`, …), with copy in `zh_cn.go` / `en.go`. **`i18n.NewError(ctx, code.Xxx)` is the constructor in use**; there is no HTTP-status variant because application errors cross the Wails frontend boundary rather than an HTTP app API.
- **Wails binding layer** — methods in `internal/app/*.go` only do: parse → call `svc.Xxx().Method(ctx, …)` → return. **Do not stuff business logic into the App struct**, otherwise go test will not cover it. Open a new file for each new domain.
- **Entity over hardcoding** — use persisted fields (type/status/icon/color/config) as the source of truth, avoiding hardcoded default values in the service that bypass the entity.

## Dependency direction (verifiable invariants)

Dependencies flow **one way**: `internal/app → service → repository → model/entity`. `internal/pkg` is the cross-cutting leaf: every layer may import it, and it may use shared model/entity types, but it must never reverse-import `service` or `repository`. These are not aspirations — each invariant below is checkable and currently holds:

```bash
VERIFY_TREE="${VERIFY_TREE:-$(git write-tree)}"   # staged proposal; set VERIFY_TREE=HEAD for a committed-tree audit

# internal/pkg must never reverse-import service / repository  → expect 0
git grep -l 'agentre/internal/\(service\|repository\)' "$VERIFY_TREE" -- 'internal/pkg/*' | wc -l

# a service must not hand-assemble queries; only two shapes are legitimate:
#   db.Ctx(ctx).Transaction(...)                       — a transaction spanning repo calls
#   db.WithContextDB(context.Background(), db.Ctx(ctx)) — carrying the handle onto a detached ctx
# anything else is a service bypassing its own repo → currently 0
git grep -n 'db\.Ctx(ctx)' "$VERIFY_TREE" -- 'internal/service/*' | grep -v 'Transaction\|WithContextDB' | wc -l

# the frontend/backend boundary is Wails-only: no HTTP-style app API  → expect 0
git grep -n 'http.HandleFunc\|gin\.' "$VERIFY_TREE" -- 'internal/app/*' | wc -l
```

**One sanctioned exception, and it is not a violation:** `internal/app/app.go` imports `agent_repo` in order to *wire* dependencies (`subagent_svc.Default().RegisterDeps(agent_repo.Agent(), …)`). `internal/app` is this project's **composition root** — assembling concrete implementations there is exactly what makes DIP and mock-injection possible. What the rule forbids is the binding layer **querying** through a repo or `db.Ctx` to serve a request; wiring at startup is not that.

## Extension recipes

Adding something of a kind that already exists? Follow the existing one rather than inventing a shape — and the fastest way to learn a recipe is to find the most recent addition of that kind and read its commit.

**A new business domain** — the full vertical, in dependency order:

1. `internal/model/entity/<domain>_entity/` — the rich entity: GORM tags, `Check(ctx)`, state predicates, field (de)serialization.
2. `migrations/YYYYMMDDNNNN_<domain>.go` — native SQL DDL, appended to the **end** of `migrationList()`.
3. `internal/repository/<domain>_repo/` — `type XxxRepo interface` + `func Xxx() XxxRepo` + `RegisterXxx(impl)`, all queries through `db.Ctx(ctx)`. Add the `//go:generate mockgen` line.
4. `internal/service/<domain>_svc/` — interface + singleton accessor + private implementation, depending only on the repo **interface**.
5. `internal/app/<domain>.go` — **one file per domain**, methods only parse → `svc.Xxx().Method(ctx, …)` → return.
6. `make generate` to refresh the Wails bindings, then the frontend consumes `frontend/wailsjs/`.

Tests come first at each layer, per [testing.md](testing.md) — repo through sqlmock, service through a mockgen mock, and no unit test for the binding.

**A new agent backend** (claude-code / codex / pi / builtin style) — entity, migration, runtime, translator, capability matrix, daemon import, frontend gating. That recipe has its own document: [agent-backend.md](agent-backend.md).

**A new cross-cutting concern** — a new single-responsibility package under `internal/pkg/<concern>`, self-contained, importing no service or repository. If it needs one, it is not cross-cutting and belongs in a domain.

**A new externally reusable CLI wrapper** — `pkg/<name>/`, maintained independently of the app's layering (this is where `claudecode` / `codex` / `piagent` live).

## Storage and paths

All desktop persistence is centralized in **AppDataDir**:

| Platform | AppDataDir                                              |
| ------- | ------------------------------------------------------- |
| macOS   | `~/Library/Application Support/agentre/`                |
| Windows | `%AppData%\agentre\`                                    |
| Linux   | `~/.config/agentre/`                                    |

The table is the installed-app default. `wails dev` / `make dev` uses the sibling leaf `agentre-dev/` instead, so development never touches production state. `AGENTRE_DATA_DIR` has highest precedence and overrides both during testing or troubleshooting. On macOS it is also a different app: `build/darwin/Info.dev.plist` sets `CFBundleIdentifier` to `com.wails.{{.Name}}.dev` and marks `CFBundleName` / `CFBundleDisplayName` with `(Dev)`, so Dock, Spaces and fullscreen do not treat it as `/Applications/Agentre.app`.

```text
<AppDataDir>/
  agentre.db          ← SQLite business database (gorm + gormigrate)
  agentre.db-wal      ← WAL journal, created next to the database (see Database and migrations)
  agentre.db-shm      ← WAL shared-memory index, same
  logs/
    agentre.log       ← full log (info+, dropped to debug+ when Debug Logging is enabled)
    error.log         ← error+ only
```

- **Business data** → SQLite, via `internal/repository/*_repo`.
- **Backend credentials and device identity seeds** → `internal/pkg/keychain`; secret values are not modeled as Wails DTO or general `env_json` fields. OpenClaw stores only non-sensitive Gateway configuration in SQLite.
- **cago runtime config** → in-memory source (`configs.WithSource(...)`), **not persisted to `config.json`**.
- **Frontend experience preferences (theme, window size, etc.)** → browser localStorage. Existing keys include `agentre.theme`, `agentre.windowSize`, `agentre.lastPath`.
- **agentred** uses a separate directory `agentred`, which can be overridden with `AGENTRED_DATA_DIR`.

Environment variables:

- `AGENTRE_ENV` — `dev` / `test` / `pre` / `prod`, defaults to `dev`.

Debug logging no longer goes through an environment variable: it is controlled by the "Settings → Version & Updates → Debug Logging" toggle, persisted as `logger.debug_enabled` in the `app_settings` table; toggling it hot-reloads the logger (takes effect immediately, no restart needed), and on startup it is restored from the persisted value.

## Database and migrations

- Driver: pure-Go SQLite (`github.com/glebarez/sqlite`, no CGO required), registered to cago's `db` component via the anonymous import `_ "github.com/cago-frame/cago/database/db/sqlite"`.
- Initialization is handled uniformly by `internal/bootstrap.Init`: register the `db.Database()` component → convert the database to WAL journal mode once (`convertToWAL`) → call `migrations.RunMigrations(db.Default())`. At runtime, get the database via `db.Ctx(ctx)`, and use `db.WithContextDB(ctx, tx)` for transactions spanning functions.
- Connection settings live in `bootstrap.sqliteDSN`: `_txlock=immediate` (every transaction opens with `BEGIN IMMEDIATE`, so a write-lock conflict goes through the busy handler instead of failing instantly the way a deferred transaction's write upgrade does) and `_pragma=synchronous(NORMAL)`. Do not add `busy_timeout` — the driver hardcodes `5000` on every connection already.
- **Journal mode is WAL**, converted once at startup outside the DSN (a `_pragma` runs on *every* connection, and a first conversion that loses the race would then break connection setup itself). The conversion is persistent, so it is a no-op afterwards; when it fails it only logs a warning and startup continues on the current mode, retrying next launch. WAL puts `agentre.db-wal` / `agentre.db-shm` next to the database — the operational consequences of that are in [debugging.md](./debugging.md#common-mistakes).

Adding a migration:

1. Create `YYYYMMDDNNNN_xxx.go` under `migrations/`, exporting `migrationYYYYMMDDNNNN() *gormigrate.Migration`.
2. Append it to the **end** of `migrationList()` in `migrations/migrations.go`. **Do not modify existing migrations**; add a patch migration when a fix is needed.
3. Prefer raw SQL for DDL (`tx.Exec(…)`), and do not rely on the implicit behavior of `AutoMigrate`.

## Generated / self-managed files

| Path                                | Producer                                 | Regenerate                  |
| ----------------------------------- | ---------------------------------------- | --------------------------- |
| `frontend/wailsjs/**`               | Wails (from `App` bindings + Go structs) | `make generate`             |
| `frontend/package.json.md5`         | Wails frontend package tracking          | `make dev` / `wails build` |
| `build/windows/installer/wails_tools.nsh` | Wails Windows installer tooling | Windows `wails build -nsis` |
| `internal/**/mock_*/`, `internal/**/mocks/`, co-located `mock_*_test.go` | `mockgen` | `make mock` |
| `frontend/dist/`                    | Vite (embedded via `//go:embed`)         | `wails build`               |
| `<AppDataDir>/agentre.db`           | gorm + gormigrate                        | auto-migrated on startup    |
| `<AppDataDir>/logs/*.log`           | cago logger                              | rolled at runtime           |

Lockfiles —— never hand-edit; use `go mod tidy` / `pnpm add|remove|install`. `frontend/wailsjs/` is gitignored.
