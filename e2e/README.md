# E2E and local verification

This directory owns two distinct workflows:

1. **Automated E2E** — `make e2e` runs the dedicated, hermetic E2E desktop composition and the three committed smoke specs.
2. **Local real verification** — `make verify-up` launches the formal desktop main with isolated local state; `drive.mjs` records one action at a time. Evidence and verdict rules are owned by [`docs/verification.md`](../docs/verification.md).

Agentre exposes no HTTP application API. Both workflows use the Wails development bridge so Chromium drives the real React → Wails IPC → Go service/repository → SQLite path.

## Automated E2E

```bash
cd e2e && pnpm run setup   # first use: dependencies + Chromium
make e2e                   # canonical local and CI entry
```

Prerequisites are Go, Node 24+, pnpm, the Wails CLI, Chromium, and the platform GUI libraries Wails needs. The suite is serial: one runner owns one desktop process and one SQLite database for the run.

`run-e2e.mjs` performs the complete lifecycle:

1. Runs only the safe automated Node guards (`run-context`, app-overlay, fake-sync, and current-contract) before launching any process. It does not run the formal verification target guard, because that tool derives installed/development roots.
2. Creates a random private run root with private data, keychain, browser, log, Playwright, manifest, and token paths.
3. Reserves dynamic loopback ports.
4. Starts the loopback fake sync HTTP server and loopback fake remote WebSocket peer.
5. Starts Vite and the independent app under `e2e/app/`; before bootstrap, the app validates the manifest/token/path contract and attests that the canonical bootstrap `AppDataDir` equals the manifest data directory.
6. After bootstrap and before E2E composition, the app attests that the runtime's canonical data directory still equals the manifest data directory.
7. Runs Playwright with one worker and exactly three specs; the desktop smoke uses SQLite `PRAGMA database_list` plus unique UI-written rows to prove the read-only oracle opened the manifest's `agentre.db`.
8. After Playwright succeeds, checks only the runner-owned file keychain and requires a regular file containing the generated remote device credential.
9. Stops only processes owned by the run.
10. Deletes successful run state; on failure, copies a sanitized per-run directory to `e2e/artifacts/<run-id>/` and removes browser/keychain/token-bearing files.

### Committed coverage

| Spec | Boundary and outcome |
|---|---|
| `tests/desktop.spec.ts` | Real UI/IPC/service/repository/migration path; unique persisted messages and SQLite `PRAGMA database_list` prove the oracle reads the manifest's `agentre.db`; deterministic streamed reply survives reload; runtime failure reaches an error terminal state. |
| `tests/sync-client.spec.ts` | Desktop sync client against a loopback protocol recorder; push/pull identity, payload, version/cursor and UI/SQLite convergence; rejection/invalid-response queue retention and visible error state. |
| `tests/remote-peer.spec.ts` | Desktop direct remote transport against a loopback binary Protobuf RPC/WebSocket peer; auth/capability/session/run protocol, streamed persistence, reconnect/disconnect terminal state, protocol-error terminal state, and credential redaction. |

No external Server, OAuth, PostgreSQL, Redis, daemon process, LAN discovery, relay, or real agent CLI is configured by this suite. Fakes bind only dynamic loopback ports and receive generated test identities and credentials.

### Isolation contract

The independent E2E app is not the production entrypoint. `e2e/preflight` validates, before bootstrap:

- the manifest and matching one-time token exist;
- the run root, data directory, and keychain directory are private and contained by the run root;
- symlink/path escapes, unsafe permissions, and production/development roots are rejected.

Before bootstrap can initialize logs, SQLite, or the file keychain, the E2E app calls the same `bootstrap.AppDataDir` resolver as normal startup and requires its canonical existing path to equal the validated manifest data directory. Immediately after bootstrap and before composition or Wails, it repeats the attestation against `bootstrap.Runtime.DataDir()`.

The runner strips inherited E2E/data/keychain variables before injecting its own values. `AGENTRE_DATA_DIR`, `AGENTRE_KEYCHAIN_DIR`, browser state, ports, fake identities, logs, traces, screenshots, and SQLite all belong to the random run. Formal `agentre` and `agentred` dependency graphs do not import this composition; `internal/guard/production_dependencies_test.go` enforces that separation and the absence of E2E build-tag seams.

The runner never enumerates, stats, opens, or compares SQLite, WAL, or SHM files outside its random run root, so the suite coexists with a running installed Agentre without touching its storage. Its post-Playwright credential check likewise searches only regular files directly inside the run's file-keychain directory and never queries a system keychain or prints the generated token.

### Artifacts and privacy

Local and CI failures print the preserved directory. CI uploads `e2e/artifacts/*/` plus the Playwright HTML report under an artifact name containing the workflow run id and attempt.

Artifacts must never contain developer tokens, system-keychain secrets, real account data, sibling-repository configuration, or complete credential-bearing protocol frames. The runner removes the isolated keychain/browser/manifest-overlay files and redacts generated run secrets from text artifacts before preservation. Protocol fakes redact the remote device token in their recorder.

### Focused checks

```bash
cd e2e && pnpm run test:guards
node --test e2e/lib/run-context.test.mjs
node --test e2e/lib/fake-sync-server.test.mjs
go test ./e2e/preflight ./e2e/composition ./e2e/fakepeer ./e2e/app
cd e2e && pnpm exec tsc --noEmit
```

The canonical automated check remains `make e2e`; passing only a focused fake or runner test does not establish the desktop smoke. `make e2e` intentionally reaches only the safe automated guard set. The explicit `cd e2e && pnpm run test:guards` command runs that set plus `lib/target.test.mjs` for the separate formal verification tool; those target guards are not reached by `make e2e`.

### File map

| Path | Role |
|---|---|
| `app/` | Independent Wails main and app metadata; starts hidden and installs E2E composition only after preflight/bootstrap. |
| `preflight/` | Manifest/token/path/permission safety gate. |
| `composition/` | E2E-only dependency composition and run-scoped fake identity seeding. |
| `fakes/` | Deterministic local runtime and login fixtures used only by the independent composition. |
| `fakepeer/`, `cmd/fake-peer/` | Loopback remote binary Protobuf RPC/WebSocket peer and executable. |
| `lib/run-context.mjs` | Random run allocation, dynamic ports, process ownership, run-scoped evidence, artifact sanitization. |
| `lib/fake-sync-server.mjs` | Loopback sync protocol fake and recorder. |
| `lib/app-overlay.mjs` | Run-local deterministic failure directive for the E2E runtime source. |
| `playwright.config.ts` | Single serial Playwright configuration and exact committed spec list. |
| `run-e2e.mjs` | Single automated orchestrator. |
| `fixtures/` | Read-only SQLite oracle and fake-sync controls. |
| `tests/` | The three committed smoke specs. |

## Local real verification

Local verification intentionally does **not** reuse the independent E2E app or its manifest. It launches the formal desktop main and preserves formal product window/runtime behavior.

```bash
make verify-up
export AGENTRE_VERIFY_SCENARIO=<scenario>
node e2e/drive.mjs snapshot
node e2e/drive.mjs click "testid=nav-settings"
node e2e/drive.mjs shot 01-settings
node e2e/drive.mjs sql "select status, count(*) from chat_sessions group by status"
node e2e/drive.mjs logs 40
make verify-down                    # retain isolated state
make verify-down VERIFY_FLAGS=--wipe
```

`make verify-up VERIFY_FLAGS=--headed` makes the attached Chromium visible; by default Chromium is headless. Both modes use the established 1440×900 driven viewport so screenshots stay legible and comparable. The formal native window follows normal product behavior and is not hidden by the launcher or driver.

`lib/target.mjs` derives one checkout-scoped data directory, file-keychain directory, browser directory, session file, bridge port, and CDP port. It rejects the installed app root, the development root, arbitrary directories, non-loopback origins, and the ordinary development bridge. A second worktree derives a different target. The launcher never adopts an unrecorded process already holding its port.

Real Server, daemon, or agent CLI behavior requires the verifier to configure and authorize that real dependency. If it is unavailable, the check fails or remains `not observed`; verification never substitutes an automated fake. Stop retains the isolated database/logs for investigation, while wipe deletes only directories first validated as this checkout's target.

`drive.mjs` attaches through the recorded CDP session, allows only same-target navigation, restricts the SQLite oracle to `SELECT`/`WITH`/`PRAGMA`/`EXPLAIN`, and writes the action ledger and screenshots under gitignored `e2e/scratch/<scenario>/`. See [`docs/verification.md`](../docs/verification.md) for when this route is warranted, report creation, evidence, authorization, redaction, and honest verdicts.
