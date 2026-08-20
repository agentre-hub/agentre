# Testing

> **The TDD/BDD-first principle** is in [`../AGENTS.md`](../AGENTS.md#high-priority-constraints-mandatory-non-negotiable), and the Red→Green→Refactor loop is [`develop.md`](./develop.md)'s. **This file is the mechanism**: how a test is designed, what to write, what not to, and how to run it.

A test is not valuable because it raised coverage. It has to **protect an observable contract**, **go red on a real regression**, and cost less to maintain than the confidence it provides.

## The Applicability Gate — Read This First

Before designing or reviewing tests, look at which rows **the contract being changed** touches. Skip the rows that do not apply **without writing "N/A" in the PR** — that is ritual, not evidence.

| Does the contract involve… | If so, read |
|---|---|
| Thresholds, counts, sizes or other edge values | [Covering the behavior space](#covering-the-behavior-space-deliberately) — edge cases |
| Invalid input, rejected dependencies, failure paths | [Covering the behavior space](#covering-the-behavior-space-deliberately) — invalid/failure |
| State held across calls, async, lifecycles | [Covering the behavior space](#covering-the-behavior-space-deliberately) — state transitions |
| Ordering, concurrency, overlapping turns or sessions | [Covering the behavior space](#covering-the-behavior-space-deliberately) — ordering/concurrency |
| Legacy formats, cross-platform differences, untrusted input, permission scopes | [Covering the behavior space](#covering-the-behavior-space-deliberately) — compatibility/security |
| Real CLI subprocesses, the daemon boundary, several layers wired together | [Choosing a test boundary](#choosing-a-test-boundary) — the integration/e2e row |
| A mechanical source-text convention (import bans, token bans, naming rules) | [Writing meaningful tests](#writing-meaningful-tests) — file-text assertions |

The rows choose **which** boundary/error case matters; they do not lower the project floor. New behavior still starts with one happy path plus at least one genuine boundary or failure case. Do not manufacture unrelated categories beyond that floor.

## State Four Things Before Writing a Test

Start from behavior, not from the current implementation:

1. **The contract** — what the caller or user can observe.
2. **The trigger** — what input, event, state or frame sequence reaches it.
3. **The outcome** — the return value, rendered state, persisted row, emitted event or external call that must result.
4. **The regression** — **a plausible wrong implementation this test would reject.**

**If you cannot state the fourth, the test is probably asserting an implementation detail or a tautology.** When fixing a bug, confirm the failure first per [`develop.md`](./develop.md)'s Fix Discipline, then write the smallest test that goes red for that confirmed cause.

### Choosing a Test Boundary

**The narrowest boundary that can observe the real contract:**

| The contract's character | Test boundary |
|---|---|
| Parsing, mapping, validation, frame decoding, state-transition logic | A pure unit test (`pkg/claudecode`, `pkg/codex`, `internal/pkg/diff`) |
| Conditional rendering, variant → token mapping, accessibility derivation | A focused component render test (`@testing-library/react` + `happy-dom`) |
| Business rules across entities | A **service** test with the repo mocked via `mockgen` |
| Persistence, the SQL actually assembled, boundary conditions | A **repository** test through `testutils.Database(t)` + sqlmock |
| Real Wails IPC, real migrations, several layers wired together | A GUI e2e spec — see [`../e2e/README.md`](../e2e/README.md); **do not stuff it into a heavily mocked unit test because that is cheaper** |
| The behavior appears impossible to automate at a meaningful boundary | Stop and revisit the seam; [one-off verification](./verification.md) may supplement tests, but replacing the regression test requires explicit user approval |

**`internal/app/` bindings are too thin to unit test** — the method only does parse → `svc.Xxx().Method` → return. **Don't stuff testable logic into `App`**, or `go test` will never see it.

### Covering the Behavior Space Deliberately

Start with the **happy path**, then add the edge or failure path most likely to change the result, per the gate above. **The floor for new behavior is one happy path + one genuine edge or failure case the contract owns.** The gate prevents unrelated test matrices; it never reduces that floor.

| Category | What it covers |
|---|---|
| Happy path | A representative valid input produces the expected observable result |
| Edge cases | Empty input, a single element; first/last; exactly at the limit; either side of a threshold; omitted optional fields; duplicates |
| Invalid/failure | Malformed input, a dependency erroring, permission denied, timeout, cancellation, partial data. Assert whether the contract rejects, reports, retries, rolls back or stays unchanged |
| State transitions | Before/after state, repeated calls, idempotency, cleanup, unsubscribing; whether a stale async result can overwrite a newer one |
| Ordering/concurrency | Out-of-order completion, overlapping turns, deduplication — **only when the production code promises these**. This repo has been bitten here repeatedly (per-session locks, lost status writes, readLoop claim order); when the code promises an ordering, test it |
| Compatibility/security | Legacy formats, platform branches (Windows subprocess attrs), untrusted paths, payload limits — **only when it is part of the contract** |

**Choose samples by equivalence class and branch, not by count:** `value === limit` / `< limit` / `> limit` producing three results means three branches, all tested; ten ordinary strings down the same path are one equivalence class with one representative.

When fixing a bug, add a regression case **close enough to the confirmed failure that putting the cause back makes it red**.

### Assert the Outcome, Not Incidental Structure

- Assert the returned domain value, the persisted row, the visible state, the accessibility attributes, the emitted event — or **necessary** collaborator calls.
- Assert a collaborator call only when **the call is itself the contract** ("must not write before approval", "publishes exactly once"). Do not assert every internal call along the path.
- **A test's name must describe what its body actually triggers and observes.** `works`, `test1` and a bare bug number are banned. The BDD form this repo uses — `Given … When … Then …` — satisfies this by construction.

## Test Stack

- **Framework** — `github.com/smartystreets/goconvey/convey` (BDD nesting) + `github.com/stretchr/testify` (assertions) + `go.uber.org/mock` (interface mocks) + `github.com/DATA-DOG/go-sqlmock` (DB mock). Use table-driven cases for branch combinations. Front end: `vitest` + `@testing-library/react` + `happy-dom`.
- **Repo unit tests: always go through `testutils.Database(t)`, never a real SQLite.** Take the `(ctx, _, mock)` triple; the business code automatically hits the mock via `db.Ctx(ctx)`. Each case asserts SQL and parameters precisely with `mock.ExpectQuery / ExpectExec`, and always ends with `assert.NoError(t, mock.ExpectationsWereMet())`. Note that **`testutils.Database` uses the MySQL dialect**, so use `` `table_name` `` backticks in the regex match; GORM `Create / Save / Update` opens a transaction automatically by default, so the match template writes `ExpectBegin / ExpectExec / ExpectCommit` (add `ExpectRollback` for the error path). **Exceptions** — exactly two places may use `t.TempDir()` to spin up a real SQLite; every other repo / service test goes through a mock:
  1. end-to-end startup tests like `internal/bootstrap/cago_test.go`, which verify the full boot + migration flow;
  2. `internal/daemon/daemon_test.go` — `agentred` opens and owns its own database, and these cases assert facts that only a real engine has. The notification journal's hard invariant is that concurrent appends each get a distinct `seq`, and it holds *because* SQLite takes a write lock for the whole single `INSERT … SELECT COALESCE(MAX(seq),0)+1 … RETURNING` statement; sqlmock replays a scripted, non-concurrent expectation list in a MySQL dialect and cannot observe that at all. Same for the WAL-vs-rollback-journal case (a catch-up read must not stall the streaming writer), the retention sweep's real row counts, and the reported database file size. The daemon's *repository* tests (`internal/daemon/repository/*`) are not exempt and do use sqlmock.
  - **Why this is a rule and not a preference**: an early repo test here used `t.TempDir()` + a real SQLite, and ended up asserting SQLite dialect side effects rather than the SQL the repository itself assembled — it stayed green through changes it should have caught. Every `testutils.Database` call goes through sqlmock; start there rather than discovering this again.
- **Service unit tests** — new or modified tests inject `mockgen` repository mocks via `RegisterXxx(mockRepo)` and do not connect to a DB. Some legacy service tests still instantiate `testutils.Database(t)` / sqlmock; they are migration debt, not a pattern to copy or expand. Move a touched test to the repository-interface seam when that migration is in scope; never use a real DB.
- **Mock generation** — add `//go:generate mockgen -source xxx.go -destination mock_xxx_repo/mock_xxx.go` at the top of the repository interface, and run `make mock` uniformly.

**Mock at external or expensive boundaries, not on every internal function.** Give the mock only the behavior this scenario needs, and **assert how our code transforms, routes, persists or responds to what the mock returned — never that the mock returned what it was configured to return.**

### Test Organization

- Use one `setupXxxTest(t)` helper per package when setup is shared. A **service** helper constructs `gomock.NewController(t)`, injects repository mocks, and returns `(ctx, mocks..., subject)`; a **repository** helper calls `testutils.Database(t)` and returns the context plus `sqlmock.Sqlmock`.
- Extract shared operations into `t.Helper()` functions, so an assertion failure points at the caller rather than inside the helper.
- **GoConvey nesting convention:** top level = feature name / method name (`Convey(..., t, func() { ... })`) → nested = scenario (success / failure / boundary) → deeper = temporal behavior ("after doing A, then do B"). Each `Convey` block runs independently, so you can safely set up the mock in the outer layer.
- **Package-level globals leak across cases.** Service singletons injected via `RegisterXxx` persist for the whole test binary, so a case that injects a mock and does not restore it can change a later case's result — and under `-race`, a goroutine left running past its case shows up as a data race in whichever test is unlucky. Re-inject inside `setupXxxTest(t)` rather than relying on the previous case's state.
- Front-end: a component that (even indirectly) imports the wails runtime needs a **per-file** `vi.mock` (`importActual` + override). **Do not add a global vite alias for it** — that breaks `App.test.tsx` / `foundation.test.tsx`, which rely on `importActual`. Go bindings do go through the global alias.

> See the cago skill (`/cago`) — complete controller / service / repo / cron / queue unit-test examples.

### Linter Exceptions (inline `//nolint`)

Per-line exceptions live beside the relevant Go statement. `.golangci.yml` separately owns the enabled linters, linter settings (`gosec` / `misspell`), path exclusions, `gofmt` / `goimports` formatter settings, and the run timeout; it does not enumerate these individual waivers.

**`//nolint:nilerr` — a deliberate alternate outcome instead of propagating the source error.** The current sites do not all share one shape, so there is no blanket exemption. Every new or modified waiver must state the caller-visible fallback it preserves, and its test must cover that degraded outcome; existing bare waivers are debt, not precedent. Existing categories include:

- carrying the failure in a response or status (`remote_device_svc`'s `LastError`, `llm_provider_svc`'s connection-test response, `httpgateway`'s status);
- optional discovery or push-event handling that fails open to an empty result / discarded malformed event (`agentskill`, `pty/remote`);
- mapping malformed local state to the domain's zero state (`project_svc` / `chat_svc` "not a git repo", `update_svc` "never checked");
- preserving the original payload when an optional transformation cannot parse it (`httpgateway/llmforward.go`).

```go
// internal/service/remote_device_svc/refresh.go
return toView(row), nil //nolint:nilerr // keychain miss is surfaced via row.LastError, not as an RPC error
```

There are currently no `//nolint:bodyclose` waivers. **`//nolint:gosec`** appears in both production and test code only where the trust/lifetime boundary is deliberate: user-authorized local commands, trusted local paths or archives, detached shutdown contexts, and credential-shaped or temp-path test fixtures. For every new or modified waiver, state the relevant G-code and concrete trust boundary inline. Existing bare waivers are debt, not precedent.

## Guard Tests

A **guard test** pins a convention that has no natural home in ordinary tests — it asserts a fact about the source tree rather than about one function's return value. This repo already runs several; follow their shape rather than inventing a new one:

| Guard | What it pins |
|---|---|
| `internal/daemon/runtime_imports_test.go` | The backends registered in the daemon's `agentruntime` registry — stops someone deleting the empty `init` import file |
| `internal/desktop/darwin_bundle_test.go` | `wails dev` macOS identity (`Info.dev.plist`) stays distinct from the installed app — stops Dock/Spaces swallowing the hidden startup window |
| `frontend/src/__tests__/i18n.test.ts` | Static `t("…")` keys resolve, and `zh-CN` / `en` expose the same key set |
| `frontend/src/__tests__/eslint-i18n.test.ts` | The i18n ESLint rule is really loaded, at the right severity and scope |
| `frontend/src/__tests__/design-tokens.test.ts` | Color tokens are used instead of literal colors |
| `frontend/src/components/agentre/__tests__/transcript-typography-guard.test.ts` | Transcript typography stays on the shared scale |

**A guard test that only greps source text is a lint rule wearing a test costume.** Where the ecosystem has a real rule available, prefer it — and when replacing a file-text assertion with a lint rule, **the rule lands and is verified before the test is deleted**.

## Changes That Do Not Introduce Behavior

There is no blanket TDD exemption for behavior changes. A genuinely behavior-preserving refactor, type cleanup, dead-code deletion, mechanical rename, or documentation-only change does not create a new production contract; prove that classification by running the existing checks. If a requested behavior appears impossible to automate (for example a platform-only lifecycle or purely visual motion), **stop and ask the user before implementation**. With explicit approval, record the manual evidence per [`verification.md`](./verification.md); never silently relabel a behavior change as exempt or add a pass-through test.

## Writing Meaningful Tests

**Do not conflate two situations.** A test failing because **the contract it asserts is wrong** (a stale fixture, an assertion wrong from the start, a contract that genuinely changed) → fix the test and state why. A test that **has never brought value whether red or green** → clean it up regardless. **Neither is a licence to weaken a valid regression test to make CI green.**

Do not write these, and delete them within the [cleanup boundaries](#scope-and-cleanup-boundaries) when you meet them (delete the test, do not touch the business logic):

- **Tautology** — asserting a constant equals its own literal definition.
- **Genuine duplication** — a file or block nearly word-for-word identical to another.
- **Redundancy** — the caller's test already covers the callee fully.
- **Pure pass-through rendering** — asserting only that a prop appeared, with no branching, mapping or derivation in the component.
- **Testing the mock or the framework** — configuring an `fn()` then asserting it returned what it was fed.
- **Name not matching content** — the body never triggers the behavior the name claims. **Worse than no test: it gives false confidence.**

These **look thin but stay**: one branch of a condition; variant → design token mapping and accessibility derivation; regression guards; the **only** coverage of a component.

### Judging the Grey Areas

| Question | How to judge |
|---|---|
| Observable contract vs implementation detail? | The caller/user can perceive this value changing → a contract. Only the source structure changed → an implementation detail. |
| A different equivalence class vs another sample? | Different only when a plausible bug would make this input produce a **different** result from the covered case. |
| A necessary collaborator call vs an internal call assertion? | Assert the call only when not calling it (or calling it wrongly) is itself the bug being guarded against. |
| A valuable thin test vs a pass-through? | Branching, mapping or derivation → thin but valuable. A prop straight through with zero conditional logic → a pass-through. |

### Scope and Cleanup Boundaries

[`develop.md`](./develop.md)'s Fix Discipline governs test cleanup too — **this makes it concrete rather than opening a loophole in it:**

| Situation | What to do |
|---|---|
| The worthless test is in a file this change already touches, or covers this change's behavior | Clean it up in passing — in scope |
| It is in a file or behavior this task does not touch | **Do not delete it in this PR.** Record it as an out-of-scope finding |
| It is a repository-wide pattern scattered across unrelated files | Do not bulk-clean here. Open a separate issue/PR scoped to that pattern |
| A replacement lint/guard test directly substitutes for the deleted test | In scope — part of "replace, then delete" |

### Cleaning Up Tests Safely

**Tests are a production dependency**, and **a failing or slow test is not automatically meaningless**. Classify before acting:

| Symptom | Classification | Handling |
|---|---|---|
| The asserted contract still holds and the production code violated it | A production regression | Fix the production code |
| The requirement genuinely changed, or the assertion was always wrong | A stale contract | Update or replace the test, recording the change |
| Timing, leaked global state or non-deterministic ordering changes the result | Flaky | Fix the root cause; **do not** add retries or raise the timeout without evidence |
| It hits a worthless category above, and deleting loses no independent regression detection | Worthless | Delete it within the cleanup boundaries |

**Verify against the source, entry by entry, before deleting** — read the production path it claims to cover (do not judge from the name), search for the same contract in neighboring tests and lint rules, state the regression it can reject, and check whether it is the only coverage of a branch, an error path or a historical regression.

## How to Run Them

**The canonical command inventory lives in [`../AGENTS.md`](../AGENTS.md#common-commands)** — this section explains placement and finishing context without maintaining a second exhaustive list.

Placement and coverage notes not repeated in that command list:

- Go tests sit next to the code (`foo_test.go`); MockGen output lives under `mock_*/`, `mocks/`, or an occasional co-located `mock_*_test.go`.
- Front-end tests live in a co-located `__tests__/` directory (the bulk) or beside the module as `<name>.test.ts(x)`.
- **Coverage:** target ≥80% for the service/repository layers of new packages. Coverage is a floor to notice gaps, never the reason a test exists.

### Finishing a Change

Per-task focused tests miss cross-package breakage (an entity change breaking another package's sqlmock SQL), whole-package goroutine-leak flakes, and type/format errors vitest never looks at. **Before calling a change done, run `make test-backend`, `make lint` and the full frontend suite, and read the real exit code** — piping through `| tail` swallows it. Editor/LSP diagnostics go stale; `tsc` is the source of truth.

## Related Documents

- The Red→Green→Refactor loop, Fix Discipline, SOLID → [`develop.md`](./develop.md)
- Driving the real app, and what a verification run must leave behind → [`verification.md`](./verification.md)
- The unified GUI e2e harness and its three committed smoke boundaries → [`../e2e/README.md`](../e2e/README.md)
- Wiring a new agent backend, with its own TDD checklist → [`agent-backend.md`](./agent-backend.md)
