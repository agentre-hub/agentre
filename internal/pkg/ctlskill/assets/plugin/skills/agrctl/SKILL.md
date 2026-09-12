---
name: agrctl
description: "Control the Agentre desktop app running on this machine — list its agents and projects, and dispatch a task to another agent. Use when asked which agents or projects exist in Agentre, when work should be handed to another agent, or when a task should run in a specific Agentre project."
---

# Agentre control channel (agrctl ctl)

`agrctl` controls the **Agentre desktop app running on this machine**: it can list the
agents and projects configured there, and hand a task to one of those agents. It resolves
the local control channel by itself from the handshake file the desktop writes, so there is
nothing to configure and no credential to pass.

The binary is already installed at this absolute path:

```
{{AGRCTL_PATH}}
```

Always invoke it by that absolute path — it is not on `PATH`.

## Commands

### List agents

```
{{AGRCTL_PATH}} ctl agents
```

One tab-separated line per configured agent: `#<id>`, name, description, and an optional
`[badge]`. Run this first to resolve the name or id that `ctl send` needs.

### List projects

```
{{AGRCTL_PATH}} ctl projects
```

One tab-separated line per project: `#<id>`, name, absolute path.

### Dispatch a task

```
{{AGRCTL_PATH}} ctl send --agent <name> [--project <id>] [--wait] [--isolated] <task text...>
```

| Flag | Meaning |
| --- | --- |
| `--agent <name>` | target agent by name, as printed by `ctl agents` |
| `--agent-id <id>` | target agent by numeric id; overrides `--agent` |
| `--project <id>` | project to run in, as printed by `ctl projects`; omitted or `0` means a free session |
| `--wait` | block until that agent's turn finishes, then print its final answer |
| `--isolated` | one-shot isolated session, not shown in the desktop sidebar |

`--agent` or `--agent-id` is required, and so is the task text. Without `--wait` the command
returns as soon as the task is dispatched and prints the new session id; the target agent
keeps working inside the desktop.

## When the desktop is not running

Every command fails with:

```
ctl: agentre desktop control endpoint not found — is the desktop app running?
```

The desktop app is not running, or has not published its control channel yet. Report that
and move on: do not retry in a loop, and do not try to launch the desktop.

## Constraints

- **`--wait` blocks for the entire remote turn.** Use it only for a short question whose
  answer is needed right now. Dispatch long-running work without `--wait` and let the user
  follow it in the desktop.
- **Never dispatch a task back to the agent you are running as.** That agent would be
  waiting on this turn while this turn waits on it, and neither side can finish. Read
  `ctl agents` and pick a different target.

Exit codes and exact output shapes: [references/commands.md](references/commands.md).
