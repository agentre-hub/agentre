# Debugging Agentre

## Overview

Agentre stores runtime state under **AppDataDir**. [architecture.md](./architecture.md#storage-and-paths) owns the platform paths and precedence: the installed app uses the `agentre/` leaf, `make dev` / `wails dev` uses `agentre-dev/`, and `AGENTRE_DATA_DIR` overrides both. Confirm which process you are debugging before reading the SQLite database and zap-formatted logs — don't add prints or run the app blind.

```text
<AppDataDir>/
  agentre.db          ← SQLite (pure-Go driver, no CGO)
  logs/
    agentre.log       ← all levels (info+; debug+ when Debug Logging is on)
    error.log         ← error+ only
```

On macOS the path has a space — **always quote it** (`"$HOME/Library/Application Support/agentre/agentre.db"`), otherwise shell word-splitting sends sqlite3 the wrong filename/arguments and the command errors or targets the wrong file.

## When to Use

- Bug report says "data disappeared / wrong value showed up" → check `agentre.db`
- Background job (cron/hook sync) misbehaving → grep `agentre.log` for the caller
- App fails to start / panics → start with `error.log`
- Migration suspected → list `migrations` table, compare against `migrations/`
- Validating a feature you just shipped → tail logs while exercising the app

**Don't use this guide for:** writing new code (use [AGENTS.md](../AGENTS.md) + [develop.md](./develop.md)), or debugging tests (use [testing.md](./testing.md)).

## Quick Reference

```bash
# Resolve data dir (installed app default shown; use .../agentre-dev for make dev)
DATA_DIR="${AGENTRE_DATA_DIR:-$HOME/Library/Application Support/agentre}"
DB="$DATA_DIR/agentre.db"
LOG="$DATA_DIR/logs/agentre.log"
ERR="$DATA_DIR/logs/error.log"

# DB — list tables, inspect schema, run query
sqlite3 "$DB" ".tables"
sqlite3 "$DB" ".schema chat_sessions"
sqlite3 -header -column "$DB" "SELECT id, name, agent_backend_id FROM agents ORDER BY id DESC LIMIT 10;"

# Applied migrations (compare against files in migrations/)
sqlite3 "$DB" "SELECT id FROM migrations ORDER BY id;"

# Tail live logs (pretty-print JSON with jq)
tail -f "$LOG" | jq -c '{ts,level,caller,msg,error}'

# Recent errors only
tail -n 200 "$ERR" | jq -c '{ts,caller,msg,error}'

# Filter by package/caller
grep -F '"caller":"hook_svc' "$LOG" | tail -n 50 | jq -c .

# Filter by a chat session (structured fields are camelCase) — desktop log only
jq -c 'select(.sessionId == 42)' "$LOG"
```

## Table → Feature Map

| Table | What lives here |
|-------|-----------------|
| `agents`, `agent_backends` | Agent definitions + which CLI backend (builtin/claudecode/codex/piagent) |
| `chat_sessions`, `chat_messages` | Conversation history, tool calls, thinking blocks |
| `llm_providers` | Provider configs (OpenAI/Anthropic/etc.) |
| `hooks`, `hook_events` | Script-driven hook definitions, schedule/run state, output events, and failure records |
| `app_settings` | UI/runtime prefs persisted by the app |
| `departments` | Org structure for the org-chart UI |
| `projects`, `project_agents`, `project_locations` | Projects, their member agents, and working-directory locations |
| `issues`, `labels`, `issue_labels` | Issue tracker (issues + labels + join) |
| `paired_agentreds` | Paired remote `agentred` (LAN daemon) records |
| `server_state` | Desktop ↔ SaaS Server connection state (single row, `id=1`) |
| `migrations` | gormigrate ledger — one row per applied migration id |

When debugging, start from the table closest to the feature, then follow FK-style id fields into adjacent tables. Schemas are not documented separately — use `.schema <table>` against the live DB.

## Reading Log Fields

The message/field contract and sensitive-data red line are owned by [observability.md](./observability.md). For investigation, start with `caller`, `msg`, and `error`, then inspect a sample line with `jq 'keys'` before filtering on a dynamic correlation field; new fields are camelCase, and guessing a key silently produces an empty result.

Tip: toggle **Settings → Version & Updates → Debug Logging** to enable debug-level logging — much more verbose, only use while reproducing a specific bug. It takes effect immediately (logger hot-reload) and survives restarts; the state lives in `app_settings.logger.debug_enabled`.

## Common Scenarios

**"Chat lost its history"** → `sqlite3 "$DB" "SELECT id, agent_id, updatetime FROM chat_sessions WHERE id=<sid>;"` then count messages; cross-check `agentre.log` around the timestamp for the calling `chat_svc/...` line.

**"Hook execution keeps warning"** → grep `agentre.log` for `caller":"hook_svc`; inspect recent hook state with `SELECT id, name, last_status, last_error FROM hooks ORDER BY last_run_at DESC;`, then query `hook_events` by `hook_id` for output/failure records.

**"DB looks stale after pulling main"** → diff applied vs. expected migrations:
```bash
diff <(sqlite3 "$DB" "SELECT id FROM migrations ORDER BY id;") \
     <(git grep -hoE 'migration[0-9]{12}' HEAD -- migrations/migrations.go | sed 's/^migration//' | sort -u)
```
Missing ids ⇒ relaunch the app to run `RunMigrations`; never hand-insert into `migrations`.

**"A remote session on `agentred` produced nothing"** → inspect `<AgentredDataDir>/logs/agentred.log` (and `error.log` for failures). `agentred run` also mirrors logs to stdout, while its remaining standard-library `log.Printf` sites are redirected into the same rolling files. The desktop and daemon share the desktop's positive `chat_sessions.id` as `sid`; the daemon isolates equal ids from different desktops by pairing that id with the authenticated peer fingerprint in its session and journal keys. Filter `agentred.log` for `sid=<id>` and the `runtime.run:` lifecycle lines, then compare the daemon-side `agentred.db` journal/session state. `agentred status` prints the daemon's database path and size. Turn on `agentred run --log-level debug` (or `AGENTRED_LOG_LEVEL=debug`) only for a bounded investigation because the daemon's debug stream is verbose.

**"App won't start"** → read `error.log` last 50 lines first. Mostly `mkdir … file exists` or `database is locked` style messages from root `main.go` and `internal/bootstrap/`.

**"make dev started but there is no window"** → Dock should show a separate **Agentre (Dev)** next to the installed Agentre (`com.wails.Agentre.dev` vs `com.wails.Agentre`). The native window is `StartHidden` until the frontend calls `WindowShow`; if Vite is still 502ing, that call never happens — check `agentre-dev/logs/agentre.log` for `app startup` and `devProxyRetryMiddleware`.

## Common Mistakes

- **Forgetting to quote the macOS path.** The space in `Application Support` makes shell word-splitting pass the wrong filename/arguments to sqlite3 — always `"$DB"`.
- **Writing to the DB while the app is running.** SQLite holds a write lock; either close the app or use `BEGIN IMMEDIATE` and accept `database is locked`. Read-only is fine.
- **Editing rows directly to "fix" a bug.** That hides the producer-side bug ([develop.md](./develop.md#fix-discipline-hard-constraint)). Reproduce, then fix the Go code + add a regression test against sqlmock.
- **Trusting `agentre.log` after a crash.** zap may buffer the last few lines. Prefer `error.log` for fatals, or turn on **Debug Logging** (Settings → Version & Updates) and reproduce.
- **Greppping with single quotes on a JSON field.** `grep '"caller":"hook_svc'` works; `grep "caller":"hook_svc"` does not (shell eats the quotes). Use `-F` for fixed strings.
- **`cp`-ing `agentre.db` alone as a "backup".** The database runs in WAL mode ([architecture.md](./architecture.md#database-and-migrations)), so recent commits may still live in `agentre.db-wal` and a lone copy is not a consistent snapshot. Copy the `-wal` / `-shm` sidecars along with it, or take the snapshot with `sqlite3 "$DB" "VACUUM INTO '/path/to/backup.db'"`.
- **Opening a copy that sits on a read-only medium.** WAL needs to create `-shm` next to the database, so the *directory* has to be writable — copying the DB to read-only media makes it unopenable rather than read-only.
- **Confusing this DB with test DBs.** Repository/service unit tests use mocks (sqlmock uses a MySQL dialect) and never touch `$DB`; the three exemptions in [testing.md](./testing.md#test-stack) (migration tests, `internal/bootstrap/cago_test.go`, `internal/daemon/daemon_test.go`) create isolated temporary SQLite databases, not this runtime DB. Bugs you reproduce here are real runtime state, not test fixtures.
