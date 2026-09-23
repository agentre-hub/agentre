# agrctl — command reference

Every command below is invoked by absolute path:

```
{{AGRCTL_PATH}} <verb> [<resource>] [flags]
```

`{{AGRCTL_PATH}} help <resource>` is the authoritative list of a resource's flags; this page
only fixes the output and error contract.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | the command succeeded |
| `1` | it failed: not found, rejected by the desktop or by the user's approval, approval timed out, or the desktop could not be reached |
| `2` | usage error — unknown command or flag, missing required flag or argument, a `--config` key the backend type does not accept, or an ambiguous name |
| `3` | a human is needed: a bare secret flag was given without a terminal (`NEEDS TTY:`) |

Errors go to stderr as `Error: <message>`; messages from the desktop (validation, "still in
use", …) are shown verbatim. Exit code `3` prints `NEEDS TTY: <explanation>` instead.

## Resources and references

| Resource | Located by | `list` filters |
| --- | --- | --- |
| `agent` | id or name (unique) | `--department <d>` |
| `department` / `dept` | id, name, or `parent/child` path | — |
| `project` / `proj` | id, name, or `parent/child` path | `--parent <p>` |
| `provider` | id or name (unique) | — |
| `model` | id or `<provider>/<model key>` | `--provider <p>` |
| `backend` | id or name (unique) | `--type <type>` |

Not found:

```
Error: agent "nobody" not found
```

Ambiguous (exit `2`):

```
Error: project "docs" is ambiguous — 2 matches:
  ID   PATH
  8    agentre/docs
  15   agentre-hub/docs
Use an ID or a parent/child path, e.g. agrctl get project agentre/docs
```

## `list`

```
{{AGRCTL_PATH}} list agents --department 研发部
ID   NAME        DEPARTMENT   BACKENDS       PINNED
3    architect   研发部        claude-local   yes
12   reviewer    研发部        claude-local   -
```

Empty cells print `-`. `-o json` prints a JSON array of the same objects `get` prints.

## `get`

```
{{AGRCTL_PATH}} get provider openrouter
{
  "id": 4,
  "name": "openrouter",
  "type": "openai-chat",
  "baseURL": "<url>",
  "enabled": true,
  "apiKey": "sk-o••••••••3f9a",
  "defaultModel": "gpt-5.1",
  "models": [ { "key": "gpt-5.1", "modelId": "openai/gpt-5.1", "contextWindow": 400000, "enabled": true } ],
  "references": { "agentBackends": 2 }
}
```

Field names match the create/update flags. References to other resources are printed as
paths you can pass straight back to a flag. API keys are masked; other secrets only print
`set` / `unset`.

## `create`, `update`, `delete`

```
{{AGRCTL_PATH}} create department --name 研发部 --parent 总部 --lead architect
department 研发部 created (id 5)

{{AGRCTL_PATH}} update agent reviewer --department 质量部
waiting for approval in this Agentre session …
agent reviewer updated

{{AGRCTL_PATH}} create model --provider openrouter --key qwen3 --model-id qwen/qwen3-coder
model openrouter/qwen3 created (id 31)

{{AGRCTL_PATH}} delete department 临时小组 --cascade
department 临时小组 deleted
```

- `update` sends only the flags given; everything else stays as it is.
- Flags that set the same field cannot be combined (`--enable` with `--disable`,
  `--config` with `--config-file`).
- `--config '<JSON>'` must be a JSON object whose keys are all listed by
  `help backend <type>`; `update` replaces only the keys it contains.
- `delete department` moves sub-departments and agents up to the parent unless `--cascade`;
  `delete provider` / `delete model` need `--force` while a backend still uses them.
  Other refusals (a project with sub-projects or active sessions, the system agent) come
  back from the desktop verbatim.

From an Agentre session every write first prints `waiting for approval in this Agentre
session …` on stderr and blocks until the user answers the approval card. A rejection:

```
Error: rejected in session #231
```

## Secrets

A bare `--api-key` (or a backend's gateway secret flag) is read from the terminal without
echo. With no terminal it exits `3`:

```
{{AGRCTL_PATH}} update provider openrouter --api-key
NEEDS TTY: --api-key reads the value from an interactive terminal.
Ask the user to run this command in their own terminal.
```

Relay that to the user as a command for their own terminal; never ask for the secret in
chat. `--api-key=<value>` also works, but the value stays in shell history.

## `send`

```
{{AGRCTL_PATH}} send --agent <name> [--agent-id <id>] [--project <id>] [--wait] [--isolated] <task text...>
```

The task text is everything after the flags; quote it so the shell keeps it as one argument
when it contains spaces.

- Fire and forget (preferred for anything long):

  ```
  {{AGRCTL_PATH}} send --agent reviewer --project 3 "review the diff on branch feat/x"
  ```

  Stdout is the new session id; stderr carries a `dispatched to …` line.

- Wait for the answer (only for a short question):

  ```
  {{AGRCTL_PATH}} send --agent reviewer --wait "which files did you touch last?"
  ```

  Stdout is that agent's final text, once its turn ends.

- One-shot session that stays out of the sidebar:

  ```
  {{AGRCTL_PATH}} send --agent-id 12 --isolated "summarize the release notes"
  ```

Reminders: `--wait` blocks for the whole remote turn, and dispatching to the agent this
session is running as deadlocks both sides.

## `acp` (run as an ACP agent)

`agrctl acp` is not a control command: it makes `agrctl` itself an **ACP v1 agent on stdio**
so an Agentre `acp` backend can drive it. Each ACP `session/new` creates one Agentre
session; each `session/prompt` is dispatched to the `--agent` target through the same
control channel the other commands use, and the turn's text / thinking / tool events stream
back as ACP `session/update` notifications. Image blocks in prompts are forwarded; audio /
resource blocks are rejected with a readable error. `session/cancel` stops the remote turn
and lets the in-flight prompt return `cancelled`.

```
{{AGRCTL_PATH}} acp --agent <name> [--agent-id <id>] [--project <id>]
```

Exit code `2` covers usage errors (missing target agent, unknown flag, unreadable
connection config). The process keeps serving until the ACP client disconnects.

Configure it in Agentre with backend type `acp`, `acpCommand = {{AGRCTL_PATH}}` and
`acpArgs = ["acp", "--agent", "<name>"]`. On a remote `agentred` box the spawned `agrctl`
must still be able to reach the desktop's control channel. Do not point such a backend at an
agent that itself uses the same backend — the dispatches recurse.
