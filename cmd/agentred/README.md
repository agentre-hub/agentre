# agentred

`agentred` is Agentre's headless compute daemon. It runs Claude Code and Codex
subprocesses on a remote macOS, Linux, or Windows machine and connects to the
desktop over binary Protobuf RPC on WebSocket.

The daemon is stateful: it keeps runtime/account configuration in `state.json`
and durable session and notification journals in its own `agentred.db` SQLite
database.

## Build from source

Build the daemon from this checkout and install it as a user service:

```bash
make agentred
build/bin/agentred --version
build/bin/agentred service install --start
build/bin/agentred service status
```

Published artifacts are listed on the [GitHub Releases](https://github.com/agentre-hub/agentre/releases)
page. The installer scripts under `scripts/` are intended for release assets.

## Upgrade in place

```bash
agentred update --check           # current / latest / whether an update exists
agentred update                   # download, verify SHA256, replace, restart
agentred update --channel beta    # stable (default), beta or nightly
agentred update --force           # upgrade even while conversations are running
```

`agentred update` resolves the latest release for the channel, downloads the
asset for this platform, verifies it against the release's `SHA256SUMS`,
atomically replaces this binary (symlinks resolved first), and then restarts the
registered service. Without a registered service it says so: a running agentred
has to be restarted before the new binary takes effect.

Every failure path leaves the current binary in place: running conversations on
the machine (reported with their count, crossed only with `--force`), a checksum
mismatch, a missing asset for this platform, and an install directory that is
not writable — that last one names the path and asks for a re-run with enough
privileges rather than installing somewhere else.

Releases are resolved from GitHub, falling back to the built-in mirrors when
GitHub is unreachable. `AGENTRED_RELEASE_BASE_URL` — the same variable
`install.sh` honours — points both at an internal or self-hosted release
directory instead.

Inside a container the replacement lives in the container's writable layer: a
restart of the same container keeps it, but recreating the container restores
the version baked into the image.

## Pair with Agentre

After the service is running, mint a one-shot pairing code:

```bash
agentred pair
# Pairing code: ABC2DE
# Expires in 300 seconds.
```

Enter the code in **Settings → Remote devices** in the desktop app. Use
`agentred status` or `agentred service status` to inspect the running daemon,
including its build version, listen URLs, active sessions, and database size.

## Service lifecycle

`agentred service` manages a user-level background service and does not require
a system-wide service installation:

| Platform | Manager |
|---|---|
| macOS | launchd LaunchAgent |
| Linux | `systemd --user` (installation attempts to enable linger and prints a repair command if host policy rejects it) |
| Windows | Task Scheduler task for the current user |

```bash
agentred service install          # install/update registration only
agentred service install --start  # install/update and start
agentred service start
agentred service status
agentred service restart
agentred service stop
agentred service uninstall
```

The generated service runs the currently installed binary's `run` command with
the resolved `AGENTRED_DATA_DIR`. Explicit run configuration is persisted, so
moving from a foreground invocation to the background service does not discard
listen or account-server settings.

## CLI subcommands

| Command | Purpose |
|---|---|
| `agentred --version` | Print `agentred <semver> (<commit>)`. |
| `agentred run [flags]` | Boot the daemon in the foreground. |
| `agentred status` | Query daemon state over local IPC (Unix socket or current-user Windows named pipe). |
| `agentred pair` | Mint a one-shot pairing code and advertise listen URLs. |
| `agentred login --server=<url>` | Claim the daemon through the account device flow and persist the server URL. |
| `agentred logout` | Log this daemon out of its account. |
| `agentred service <action>` | Manage the user-level background service. |
| `agentred update [--check] [--channel=<name>] [--force]` | Download, verify and install the latest release, then restart the registered service. |
| `agentred llm list` | List LLM providers without printing raw API keys. |
| `agentred llm add --key=<uuid> --name=<name> --type=<type> --api-key=<key>` | Add or update an LLM provider. |
| `agentred llm remove --key=<uuid>` | Delete an LLM provider. |
| `agentred claudecode <args...>` | Internal Claude Code hook passthrough used by spawned subprocesses. |

`agentred run` accepts `--host`, `--port`, `--tls-cert`, `--tls-key`,
`--server`, and `--log-level`. Resolution order is explicit flag, environment,
persisted state, then default. The corresponding environment variables are
`AGENTRED_HOST`, `AGENTRED_PORT`, `AGENTRED_TLS_CERT`, `AGENTRED_TLS_KEY`,
`AGENTRED_SERVER_URL`, and `AGENTRED_LOG_LEVEL`.

## Logs

`agentred run` writes JSON logs to `<AppDataDir>/logs/` and to stdout. The
service managers do not capture stdout on macOS, so the files are the only
record there:

```text
<AppDataDir>/logs/
  agentred.log   everything at the active level
  error.log      error and above only
```

The daemon's remaining standard-library `log.Printf` sites (panic recovery,
shutdown failures, restart sweeps) are redirected into the same files.

Both files roll at 30 MB, keep 10 backups and 30 days, and are not compressed —
so each caps out around 330 MB on disk. `--log-level` (or `AGENTRED_LOG_LEVEL`)
takes `debug`, `info` (default), `warn`, or `error`; an unknown value is a usage
error rather than a silent fallback. Debug is verbose enough to fill and roll
those files in minutes, so turn it on for an investigation, not permanently.

## Storage layout

`agentred` uses a data directory separate from the desktop app:

| Platform | Path |
|---|---|
| macOS | `~/Library/Application Support/agentred/` |
| Linux | `~/.config/agentred/` |
| Windows | `%LOCALAPPDATA%\agentred\` |

Important files are:

```text
<AppDataDir>/
  state.json    runtime state, listen preferences, account claim, and LLM providers
  agentred.db   SQLite session and notification journals
  logs/         rolling agentred.log and error.log (see Logs above)
```

Local CLI IPC uses `agentred.sock` on Unix and a current-user named pipe on
Windows. Override the data directory for testing or operations with
`AGENTRED_DATA_DIR`.

## Encryption

By default the LAN endpoint uses `ws://`. Supply both `--tls-cert` and
`--tls-key` to use `wss://`. A locally trusted certificate can be generated with
`mkcert`; the desktop supports OS trust, leaf-certificate pinning, a custom CA
bundle, and an explicit development-only skip-verification mode.
