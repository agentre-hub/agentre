# Observability

> Tests prove the behavior you asserted holds; observability is **whether you can still see what happened when something goes wrong**. With no convention for where to instrument and at what level, you get one of two extremes: nothing logged (guesswork when it breaks), or everything logged (zero signal-to-noise, which amounts to the same).
>
> **Scope**: this file owns the **logging convention** — the entry point, levels, where to instrument, and the message/field format. The **commands** for reading logs, querying SQLite and reproducing a production bug are [`debugging.md`](./debugging.md)'s; what a verification run must record is [`verification.md`](./verification.md)'s.
>
> Agentre implements **no application metrics and no distributed tracing**. Transitive Prometheus / OpenTelemetry modules exist in `go.mod`, but production Go code does not import or instrument them. Do not add a metric or span "for completeness"; introducing that behavior is a deliberate product and operations decision, not a gap to fill.

## The Single Entry Point

**Use cago's zap wrapper `github.com/cago-frame/cago/pkg/logger` for application and service logging.** Levels, structured fields, the output destination and file rotation are then implemented in one place. `fmt.Print*` / `fmt.Fprint*` are reserved for command, hook-protocol, process-boundary, or e2e-fake stdout/stderr — output consumed by a person or protocol, not operational logging. The standard library `log` remains only at process bootstrap/fatal/panic and daemon-process boundaries where cago logging may not yet be available. Do not copy either boundary exception into domain code.

- `logger.Ctx(ctx)` when you have a ctx — **the default**, because it carries request-scoped context.
- `logger.Default()` only where there is no ctx (bootstrap, `gogo.Go`). It re-reads the current level, so the Debug toggle takes effect without a restart.

Output goes to `<AppDataDir>/logs/agentre.log`, dropping to debug-and-above once **Settings → Version & Updates → Debug Logging** is on. The `agentred` daemon shares the same construction point (`internal/pkg/logfile`) and writes `<AgentredDataDir>/logs/agentred.log`, with its level set by `agentred run --log-level` / `AGENTRED_LOG_LEVEL`. Both hosts keep an `error.log` sidecar and roll at 30 MB × 10 backups × 30 days.

There is no lint rule pinning this boundary — review new logging sites deliberately rather than treating existing bootstrap or CLI-output exceptions as precedent.

## How to Choose a Level

**A level is a filter for the reader, not an emphasis marker for the author.**

| Level | When to use it | Counter-example |
|---|---|---|
| `Error` | State may be corrupted; fields must be enough to reconstruct the scene | Invalid user input — that is a normal business branch |
| `Warn` | Recoverable error, a swallowed `defer` error, a degradation / fallback / retry | Anything emitted on every turn |
| `Info` | A business milestone — what you reconstruct "what happened" from | Inside a loop, or on every function entry/exit |
| `Debug` | Redacted normal-path detail needed only while investigating (event kind, timing, stop reason) | Sensitive data or complete agent frames (**banned at every level**) |

- **An `Error` must be actionable.** One nobody will act on trains everyone to ignore `Error`, and then the real incident drowns. If no human is needed, demote it.
- **Having logged an error, do not also return it unlogged up the chain and log it again** — one failure recorded three times up the call chain is noise. **Log it once, at the layer that can decide the thing failed.**

## Where to Instrument (mandatory on critical paths)

1. **Lifecycle boundaries** — session / turn / runtime start-stop, abort, auto-continue.
2. **External call boundaries** — CLI spawn/exit, remote borrow/return, HTTP, migrations, filesystem.
3. **Degradation / fallback / retry.**
4. **At the owning service/repo failure boundary** — log an unexpected critical failure only where that layer can decide it failed and no higher layer will log the same error.
5. **State changes** — permission mode, login, remote connection.

**What not to log:** inside a loop; every function entry/exit; anything derivable from the next line of code; **sensitive data**.

## Message and Field Format

- **Message: lowercase, with a `package.Method:` prefix**, so the emitting site is greppable.
- **Dynamic values go into structured `zap.Xxx(...)` fields — never `fmt.Sprintf` into the message.** Concatenated, they cannot be filtered or aggregated.
- **Field names are camelCase.** New or modified fields follow this rule. A few existing `hook_svc` sites still use the legacy `hook_id`; they are known exceptions, not a pattern to copy.

```go
// ✅ real usage, lifted from internal/service/chat_svc/chat.go
logger.Ctx(ctx).Warn("chat_svc.Stop: runner.Abort failed",
    zap.Int64("sessionId", req.SessionID),
    zap.String("backendType", be.Type),
    zap.Error(aerr))

// ❌ concatenation — cannot be filtered or aggregated by sessionId
logger.Default().Warn(fmt.Sprintf("stop failed for %d: %v", req.SessionID, aerr))
```

- **Carry a correlation id.** Under concurrency — several sessions streaming at once is the normal state here — a line with no `sessionId` / `providerSessionID` / `requestId` is nearly unreadable.

## What Must Never Reach the Logs (hard red line)

**Passwords, tokens, API keys, cookies, private keys, complete credentials, and complete request/response bodies.**

- Where an identifier is needed, log a **redacted form**. The existing precedent is `maskedTail` (`internal/daemon/handlers/llm.go`) for provider API keys, and `sanitizeTunnelHeaders` (`internal/daemon/handlers/mcpproxy.go`) for forwarded headers — reuse them rather than writing a third.
- Logs reach log files, issue attachments, and an AI agent's context — **far more readers than you imagine.**
- Runtime Debug sites that serialize complete agent frames (e.g. `claudecode/session.go` and `codex/session.go`) are **sanctioned, intended behavior**, not debt: Debug Logging is an opt-in toggle (off by default), and turning it on exists precisely to capture the full frame for troubleshooting — redacting the payload there would remove the toggle's entire purpose. This does not relax the credential red line above: a full frame must still never carry a raw password / token / API key / cookie / private key: any producer that could embed one still owes the frame a redaction step before this site logs it.
- `piagent`'s equivalent sink logs only metadata, not because it follows a different convention: `pkg/piagent/client.go:504` hands the sink a diagnostic projection that is already payload-free (see the comment at `client.go:54`) — that layer never receives the raw frame to begin with. Do not read this difference between the three backends as drift awaiting consolidation.

## Using Logs to Verify and Reproduce

- **Turn Debug logging on first** (Settings → Version & Updates). When something cannot be reproduced, saying **where it got to and which branch it did not enter** beats "cannot reproduce".
- **For what the UI cannot drive** — background turns, autonomous continuations, daemon round-trips — reach a verdict through observable side effects: assert that a **specific** structured line appeared, and cross-check the DB. **"No errors" is not evidence.**
- Paste only the deciding, redacted lines into the verification report; link a full capture only after redacting it too — see [`references/verification-report-template.md`](./references/verification-report-template.md).

The concrete commands (filtering with `jq`, reading the SQLite tables, the macOS `Application Support` quoting trap) live in [`debugging.md`](./debugging.md) — they are not repeated here.

## Related Documents

- The Red→Green→Refactor loop, Fix Discipline, SOLID → [`develop.md`](./develop.md)
- Commands for reading logs and reproducing production bugs → [`debugging.md`](./debugging.md)
- Confirming a change works, and what to record → [`verification.md`](./verification.md)
- How tests are designed → [`testing.md`](./testing.md)
