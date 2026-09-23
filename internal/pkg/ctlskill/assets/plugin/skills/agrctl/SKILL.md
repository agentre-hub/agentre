---
name: agrctl
description: "Manage and drive the Agentre desktop app running on this machine — list, inspect, create, update and delete its agents, departments, projects, LLM providers, models and agent backends, and dispatch a task to another agent. Use when asked what exists in Agentre, when Agentre's configuration should change, when work should be handed to another agent, or when a task should run in a specific Agentre project."
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
{{AGRCTL_PATH}} send --agent <name> [--project <id>] [--wait] [--isolated] <task text...>
```

Resources: `agent`, `department` (`dept`), `project` (`proj`), `provider`, `model`, `backend`.
`list` also accepts the plural (`agents`, `providers`, …).

**Before creating or changing anything, run `help <resource>`** (and `help backend <type>`
for a backend). It prints the exact flags, the fields `--config` accepts, which flags are
secrets, and examples. It works without the desktop.

### Referring to a resource

`<ref>` is a numeric id, a name, or a `parent/child` path. Agents, providers and backends
have globally unique names; projects and departments are only unique among siblings, so
use a path such as `agentre/docs` when a bare name is ambiguous. A model is always
`<provider>/<model key>`. Flags that point at another resource (`--department`, `--parent`,
`--backend`, `--provider`, `--lead`, `--add-member`, …) take the same forms.

When a name matches several resources the command fails with exit code `2`, lists every
candidate's id and path, and prints a corrected command to run instead.

### Reading

- `list <resource>` prints a table whose first column is the id; `-o json` prints a JSON
  array. Filters: `list agents --department <d>`, `list projects --parent <p>`,
  `list models --provider <p>`, `list backends --type <type>`.
- `get <resource> <ref>` prints JSON whose field names match the create/update flags. API
  keys are masked, other secrets only say whether they are set.

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
stderr shows `waiting for approval in this Agentre session …` meanwhile. A rejection ends
with exit code `1`. Do not retry a rejected write.

### Secrets

Secret flags (`--api-key` of a provider, the OpenClaw gateway secret of a backend) are read
from the user's terminal when written bare. You have no terminal, so a bare secret flag
ends with exit code `3` and a `NEEDS TTY:` message. **Never ask the user to paste a secret
into the conversation** — tell them to run that exact command in their own terminal.

### Dispatch a task

```
{{AGRCTL_PATH}} send --agent <name> [--project <id>] [--wait] [--isolated] <task text...>
```

| Flag | Meaning |
| --- | --- |
| `--agent <name>` | target agent by name, as printed by `list agents` |
| `--agent-id <id>` | target agent by numeric id; overrides `--agent` |
| `--project <id>` | project to run in, as printed by `list projects`; omitted or `0` means a free session |
| `--wait` | block until that agent's turn finishes, then print its final answer |
| `--isolated` | one-shot isolated session, not shown in the desktop sidebar |

`--agent` or `--agent-id` is required, and so is the task text. Without `--wait` the command
returns as soon as the task is dispatched and prints the new session id; the target agent
keeps working inside the desktop.

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

## Constraints

- **`--wait` blocks for the entire remote turn.** Use it only for a short question whose
  answer is needed right now. Dispatch long-running work without `--wait` and let the user
  follow it in the desktop.
- **Never dispatch a task back to the agent you are running as.** That agent would be
  waiting on this turn while this turn waits on it, and neither side can finish. Read
  `list agents` and pick a different target.

Exit codes and exact output shapes: [references/commands.md](references/commands.md).
