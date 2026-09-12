# Feature verification

Confirming a change works — or that a bug reproduces — by driving the formal running desktop, and defining what that run leaves behind. Harness mechanics are owned by [`../e2e/README.md`](../e2e/README.md); test design by [testing.md](testing.md); log and SQLite investigation by [debugging.md](debugging.md).

## When to skip this route

Use targeted committed tests when they fully observe the changed logic: pure logic, parsers, reducers, protocol decoding, docs, comments, types, and behavior already established by the committed suite. Use real verification when the claim depends on cross-process/platform wiring that an automated test cannot establish: formal Wails startup, a real Server or daemon, a real agent CLI subprocess, gateway integration, or a platform-specific window/lifecycle effect.

This route does not replace TDD. A reproducible bug still owes the committed failing regression test required by [AGENTS.md](../AGENTS.md).

## The route

**Start one formal desktop, drive it, record as you go, then stop it.** Do not author a one-off spec.

```bash
make verify-up
export AGENTRE_VERIFY_SCENARIO=<scenario>

node e2e/drive.mjs snapshot
node e2e/drive.mjs click "testid=nav-settings"
node e2e/drive.mjs shot 01-settings
node e2e/drive.mjs sql "select status, count(*) from chat_sessions group by status"
node e2e/drive.mjs logs 40

make verify-down                    # retain isolated state for investigation
make verify-down VERIFY_FLAGS=--wipe
```

Use `make verify-up VERIFY_FLAGS=--headed` when the attached Chromium itself must be visible. Chromium is otherwise headless; both modes keep the established 1440×900 driven viewport for comparable evidence. The formal native desktop window keeps product behavior; the verification launcher does not hide or otherwise alter it.

1. Run the narrow committed tests and type checks first. Run broader backend/frontend gates when the blast radius or repository gate requires them.
2. Create `e2e/scratch/<scenario>/report.md` from [references/verification-report-template.md](references/verification-report-template.md) **before** starting acceptance/reproduction evidence.
3. Bring up the target. `make verify-up` launches the formal main with checkout-scoped `AGENTRE_DATA_DIR`, file keychain, browser directory, bridge/CDP ports, and gateway port override. Never hand-start the app against your own data directory.
4. Configure only the real dependencies the claim requires. The verifier may explicitly configure a real `agentre-server`, `agentred`, Claude Code, Codex, or Pi CLI. If the real dependency is unavailable, record the check as failed or `not observed`; do not replace it with a fake.
5. Drive one action per command. `drive.mjs` appends commands/outcomes to `logs/drive.log`, writes screenshots to `screenshots/`, and offers a read-only SQLite oracle independent of the app service layer.
6. Capture deciding observations while the run is alive. Stop retains the isolated database and logs by default, but external state and transient process output may disappear.
7. Fill the verdict table last, keeping failed and unreached checks visible. Then stop the launcher and account for retained/wiped state.

A quick private look may use the default `_unscoped` ledger and be deleted immediately. A spec acceptance, bug reproduction, or result reported to another person requires the scenario directory and report.

## Choosing the observation

| Change lands in | Reach it with | Independent oracle |
|---|---|---|
| `agrctl` or `agentred` | run the formal binary | full command, exit code, deciding stdout/stderr or read-only status query |
| GUI or Wails IPC | formal desktop via `verify-up` + `drive.mjs` | read-only query against the isolated `agentre.db`, plus screenshots/action ledger |
| real agent CLI behavior | formal desktop with that CLI actually installed/configured | redacted event-kind/timing/stop-reason lines plus SQLite/UI state |
| real Server or daemon integration | formal desktop plus the explicitly configured real process | both ends' redacted logs/status and independent persisted-state reads |
| migration | forward command against a database containing real existing rows | before/after query, row counts/edge values, rollback or restore evidence |

In every form, one observation comes from a path the driven surface does not share. A successful UI message alone cannot prove the database write happened; a client log alone cannot prove the peer persisted the request.

## What the route guarantees and refuses

The launcher and driver enforce these rules:

- **Formal entry only.** Verification launches the repository root desktop main, not the independent E2E app and not an E2E manifest/composition.
- **Throwaway local state only.** [`../e2e/lib/target.mjs`](../e2e/lib/target.mjs) allow-lists one checkout-scoped data directory and rejects installed, development, arbitrary, and other-checkout roots.
- **File keychain only for the run.** The launcher creates a private keychain directory before startup. An unsafe or missing configured directory makes bootstrap fail; it never falls back to the system keychain.
- **Own origin only.** The driver accepts only the recorded loopback bridge and rejects the normal development bridge and external origins.
- **Read-only DB oracle.** Only `SELECT`, `WITH`, `PRAGMA`, and `EXPLAIN` statements are accepted.
- **No process adoption.** A process holding the checkout's verification port is never driven. It is stopped only when every holder is proven to be this checkout's own orphan — this checkout's path on its command line and a working directory inside the checkout — which is what a killed `wails dev` leaves behind, since the app binary it launched keeps the port. Anything else, including a holder the operating system will not describe or a platform without `lsof`, is refused with no signal sent at all.
- **Worktree isolation.** Target paths, session file, and ports derive from the checkout path, so distinct worktrees do not share verification state.
- **Real dependency honesty.** There is no fake mode or fallback. An unavailable external dependency means the corresponding criterion failed or was not observed.
- **Scoped cleanup.** Stop acts on recorded process IDs, plus leftover processes checked one at a time and kept only when they run a `vite` executable that resolves inside this checkout's `frontend/`. A name that merely contains `vite`, such as a `vitest` run of this checkout, is never signalled. Wipe deletes only target directories that pass the isolation allow-list.

## Safety, authorization, and privacy

Never weaken an assertion, skip a failed step, or describe red as green. `holds` means the deciding runtime observation happened; `not observed` names exactly what prevented it.

Obtain authorization before destructive or externally visible effects: writing a real Server account, pairing/claiming a daemon, modifying remote projects, invoking paid agent APIs, or running a migration over real rows. State the blast radius and cleanup before execution.

Redact before saving and again before embedding: tokens, cookies, device secrets, real credentials, personal paths when unnecessary, complete protocol frames, and contents of the real installed/development databases. Verification evidence must never contain sibling-repository configuration copied merely to make a run pass.

## Maintaining this route

After changing the launcher, driver, paths, or docs, follow [documentation.md](documentation.md) and run:

```bash
git grep --cached -n 'e2e/scratch/' -- .gitignore
git grep --cached -n -A2 '^verify-up:' -- Makefile
git grep --cached -n 'assertIsolatedDataDir' -- e2e/lib/target.mjs
cd e2e && pnpm run test:guards
git ls-files --cached --error-unmatch e2e/drive.mjs e2e/verify.mjs docs/references/verification-report-template.md
```
