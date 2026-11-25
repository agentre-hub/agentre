---
name: agrctl
description: "Manage and drive the Agentre desktop app running on this machine — list, inspect, create, update and delete its agents, departments, projects, LLM providers, models and agent backends, dispatch a task to another agent, continue, wait for, follow or stop a session, and read and write the agent's memory. Use when asked what exists in Agentre, when Agentre's configuration should change, when work should be handed to another agent, when a task should run in a specific Agentre project, or when something should be remembered for later sessions or looked up in memory."
---

# Agentre control CLI (agrctl)

`agrctl` manages the **Agentre desktop app running on this machine**: it lists and edits the
resources configured there (agents, departments, projects, LLM providers, models, agent
backends) and hands tasks to its agents. It finds the local control channel by itself, so
there is nothing to configure and no credential to pass.

The binary is already installed at this absolute path:

```
{{AGRCTL_PATH}}
```

Always invoke it by that absolute path — it is not on `PATH`.

## Commands

```
{{AGRCTL_PATH}} list <resource> [filters] [-o json]
{{AGRCTL_PATH}} get <resource> <ref>
{{AGRCTL_PATH}} create <resource> [flags]
{{AGRCTL_PATH}} update <resource> <ref> [flags]
{{AGRCTL_PATH}} delete <resource> <ref> [flags]
{{AGRCTL_PATH}} help [<resource> [<backend type>]]
{{AGRCTL_PATH}} send --agent <name> [--project <ref>] [--wait] [--isolated] <task text...>
{{AGRCTL_PATH}} send --session <id> [--steer] [--wait] <text...>
{{AGRCTL_PATH}} wait <session> [--timeout <duration>] [-o json]
{{AGRCTL_PATH}} tail <session> [--from <cursor>] [--until-idle]
{{AGRCTL_PATH}} stop <session>
{{AGRCTL_PATH}} list sessions [--agent <a>] [--project <p>] [--device <d>] [--status running|idle|waiting|failed] [--limit <n>] [-o json]
{{AGRCTL_PATH}} get session <session>
{{AGRCTL_PATH}} messages <session> [--last <n>] [--before <cursor>] [--full] [-o json]
{{AGRCTL_PATH}} pending [<session>] [-o json]
{{AGRCTL_PATH}} answer <session> <id> (--option <text|number> | --text <answer> | --allow | --deny [--message <reason>])
```

Resources: `agent`, `department` (`dept`), `project` (`proj`), `provider`, `model`, `backend`,
`memory`. `list` also accepts the plural (`agents`, `providers`, `memories`, …).

**Before creating or changing anything, run `help <resource>`** (and `help backend <type>`
for a backend). It prints the exact flags, the fields `--config` accepts, which flags are
secrets, and examples. It works without the desktop.

### Referring to a resource

`<ref>` is a numeric id, a name, or a `parent/child` path. Agents, providers and backends
have globally unique names; projects and departments are only unique among siblings, so
use a path such as `agentre/docs` when a bare name is ambiguous. A model is always
`<provider>/<model id>`, split at the first `/` (so `openrouter/openai/gpt-5.1` is model id
`openai/gpt-5.1` of provider `openrouter`); if one provider has the same model id twice, use
the numeric id. `create model` takes `--provider` and `--model-id`; the model key is generated
by Agentre and only shown by `get model`. Flags that point at another resource (`--department`, `--parent`,
`--backend`, `--provider`, `--lead`, `--add-member`, …) take the same forms.

When a name matches several resources the command fails with exit code `2`, lists every
candidate's id and path, and prints a corrected command to run instead.

### Reading

- `list <resource>` prints a table whose first column is the id; `-o json` prints a JSON
  array. Filters: `list agents --department <d>`, `list projects --parent <p>`,
  `list models --provider <p>`, `list backends --type <type>`.
- `get <resource> <ref>` prints JSON whose field names match the create/update flags. API
  keys are never printed — `apiKeySet` only says whether one is set; other secrets only say
  `set` / `unset`, or `unknown` when the device that keeps them cannot be asked (offline, or
  managed through the server).

### Writing

- `create` prints `<resource> <name> created (id N)`.
- `update` changes **only** the flags you pass and prints `<resource> <name> updated`.
  `update project … --add-member/--remove-member` edits members; `update agent … --backend`
  (repeatable) replaces all execution targets; `update model … --default`, `--enable`,
  `--disable`.
- `delete` prints `<resource> <name> deleted`. Deleting a department moves its children up
  unless `--cascade`; a provider or model still used by a backend needs `--force`.
- Complex backend settings go in `--config '<JSON>'` or `--config-file <file>`; only the
  keys listed by `help backend <type>` are accepted.

Every write you make from this session **waits for the user's approval in this session**;
stderr shows `waiting for approval in session #<id> …` meanwhile. (Outside any session, a
program without a terminal, or one run by an agent CLI such as Claude Code, Codex, Gemini
CLI or opencode even inside a terminal, sees `waiting for approval in the Agentre desktop …`
instead.)
A rejection ends with exit code `1`. Do not retry a rejected write.

This approval is a guard rail for well-behaved agents, not a security boundary: any process
running as the same OS user can read the desktop's handshake file, fake a terminal, or strip
the agent CLI's environment markers and skip it. Never try to do any of these.

### Memory

Memory holds facts that carry over to your later sessions. Inside an Agentre session, unlike
every other resource, its writes **take effect at once, with no approval**; outside one they
follow the same approval rules as everything else.
`help memory` is the full usage and the rules for what is worth remembering — read it before
your first write.

```
{{AGRCTL_PATH}} list memory [--layer <layer>] [--query <text>]
{{AGRCTL_PATH}} get memory <id>
{{AGRCTL_PATH}} create memory --summary "<one line>" [--detail "<text>"] [--layer <layer>] [--department <d> | --agent <a> | --project <p>]
{{AGRCTL_PATH}} update memory <id> [--summary "<one line>"] [--detail "<text>"]
{{AGRCTL_PATH}} delete memory <id>
```

- A memory is located by its id, `m58` or `58`, as shown in your session's memory section
  and by `list memory`.
- `list` and `get` see only the memories of this session: global, your departments, yourself
  and this session's project. `CARRIED no` marks one that did not fit its layer's budget and
  so was not carried into this session. `--query` searches summaries and details.
- `create` writes to you by default; `--layer department | project | global` picks another of
  your layers, and `--department <d>` one of the departments above yours. Writing to a
  layer that is not yours is refused. The same summary twice in one layer is not added again.
- `update` and `delete` work on any memory you can see, including the user's. `delete`
  archives; the user can restore it, and the previous version of an update is kept.
- Each write prints one line: `memory m<id> created|already exists|updated|archived (<layer>)`.
  A second line `over budget: <layer> is <N> characters over its memory budget; …` means you
  should merge or shorten that layer's memories.
- What you write now reaches new sessions, not this one.
- Outside a session every layer is visible and `create` needs a named layer: `--layer global`,
  `--department <d>`, `--agent <a>` or `--project <p>`.

### Secrets

Secret flags (`--api-key` of a provider, the OpenClaw gateway secret of a backend) are read
from the user's terminal when written bare. You have no terminal, so a bare secret flag
ends with exit code `3` and a `NEEDS TTY:` message. **Never ask the user to paste a secret
into the conversation** — tell them to run that exact command in their own terminal.

### Dispatch a task and follow the session

```
{{AGRCTL_PATH}} send --agent <name> [--project <ref>] [--wait] [--isolated] <task text...>
```

| Flag | Meaning |
| --- | --- |
| `--agent <name>` | target agent by name (or numeric id), as printed by `list agents` |
| `--agent-id <id>` | target agent by numeric id; overrides `--agent` |
| `--project <ref>` | project to run in: name, `parent/child` path or id from `list projects`; omitted means a free session |
| `--device <device>` | new session: run on the agent's first backend on that device (name or fingerprint; `local` is this desktop) |
| `--backend <ref>` | new session: run on exactly that backend of the agent (name or id); not with `--device` |
| `--title <title>` | title of the new session |
| `--session <id>` | continue that session instead of starting one (no `--agent` / `--device` / `--backend` / `--project` / `--isolated` / `--title`) |
| `--steer` | the session is running: interject into its current turn (cannot carry `--image`) |
| `--image <file>` | attach a local png / jpeg / gif / webp image; repeatable |
| `--model <provider>/<model>`, `--cli-model <model>`, `--effort <level>`, `--permission-mode <mode>` | change this session's setting from the next turn on; a value the backend does not support fails with the choices listed (exit `2`) |
| `--wait [--timeout <duration>]` | block like `wait` below |
| `--isolated` | one-shot isolated session, not shown in the desktop sidebar |
| `-o json` | print one JSON object with `session`, `status` and `text` / `error` / `pending` |

Without `--device` / `--backend` a new session runs on the agent's first available backend. A
device or backend the agent does not have fails with the choices listed (exit `2`); one that is
offline, or has no path for the project, fails with the reason (exit `1`).

`--agent` (or `--agent-id`) or `--session` is required, and so is the text. Without `--wait`
the command returns as soon as the task is dispatched and prints the **session id** — a full
UUID; every other session command takes exactly that id. The target agent keeps working inside
the desktop.

A running session refuses a plain `send --session` (exit `1`, "session is running"): add
`--steer` to interject, or `wait` first. Steers not delivered when the turn ends go out, in
order, at the start of the next one.

- `wait <session>` returns when the session is idle (stdout: the final text of the last turn,
  exit `0`; a failed turn prints its error, exit `1`), waits for input (stdout: the pending
  items as JSON, exit `4`), or `--timeout` passes (exit `5`, still running — wait again).
- `tail <session>` prints the session's events as NDJSON lines, each with an opaque `cursor`;
  `--from <cursor>` resumes after it, `--until-idle` exits when the session goes idle (`0`) or
  waits for input (`4`).
- `stop <session>` stops the running turn and drops the steers not yet delivered, printing how
  many. You cannot stop a session from inside an Agentre session.
- `list sessions`, `get session`, `messages` and `pending` read any session on this desktop
  (`commands.md` has the output shapes). From inside an Agentre session you can read, but
  `answer` is refused: pending items are answered by a person or by a program outside a session.

## Running as an ACP agent (`agrctl acp`)

Besides controlling the desktop, `agrctl` can itself run as an **ACP v1 agent** on stdio. An
Agentre `acp` backend can then drive it, so a task dispatched to that backend is handed to
another agent configured in this desktop:

```
{{AGRCTL_PATH}} acp --agent <name> [--project <id>]
```

| Flag | Meaning |
| --- | --- |
| `--agent <name>` | target Agentre agent; prompts are dispatched to it (required) |
| `--agent-id <id>` | target by numeric id; overrides `--agent` |
| `--project <id>` | project to run in; omitted or `0` means a free session |

To use it, add an Agentre backend of type **acp** with `acpCommand = {{AGRCTL_PATH}}` and
`acpArgs = ["acp", "--agent", "<name>"]`. It reads the same control channel as the other
commands, so the desktop must be running.

**Do not point an `acp` backend at an `agrctl acp` whose target agent itself uses that same
backend** — the dispatches recurse until a turn waits on itself. Pick a different target
agent.

## When the desktop is not running

Every command except `help` fails with:

```
Error: agentre desktop control endpoint not found — is the desktop app running?
```

The desktop app is not running, or has not published its control channel yet. Report that
and move on: do not retry in a loop, and do not try to launch the desktop.

On a remote execution host (agentred) the message is instead:

```
Error: this is an agentred host — only Agentre-dispatched sessions can use agrctl here (…)
```

agrctl on that host only works inside a session Agentre dispatched to it. Report that and
move on; there is no desktop to start there.

## Constraints

- **`--wait` and `wait` block until the session settles.** Use them only for a short question
  whose answer is needed right now. Dispatch long-running work without `--wait`. Foreground
  shell tools time out (Claude Code's foreground Bash stops after 10 minutes), so for a long
  turn run `wait <session>` (or the `--wait` call) as a background shell command, or give
  `--timeout` and wait again on exit `5`.
- **Never dispatch a task back to the agent you are running as**, and never `send --session`
  to the session you are in (refused, exit `2`). That agent would be waiting on this turn while
  this turn waits on it, and neither side can finish. Read `list agents` and pick a different
  target.
- Exit code `4` means the session waits for a person to answer; you cannot answer it from
  inside a session. Tell the user, then `wait` again.

Exit codes and exact output shapes: [references/commands.md](references/commands.md).
