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
| `1` | it failed: not found, rejected by the desktop, the account or the user's approval, approval timed out, the session is running, a turn failed, the login expired, or the desktop or the account could not be reached |
| `2` | usage error — unknown command or flag, missing required flag or argument, a `--config` key the backend type does not accept, an ambiguous name, a session reference that is not a full session UUID, a value the target backend does not support, or sending to your own session |
| `3` | a human is needed: a bare secret flag was given without a terminal (`NEEDS TTY:`) |
| `4` | the session waits for input; stdout holds the pending items as JSON |
| `5` | `--timeout` passed while waiting; the session is still running |

Errors go to stderr as `Error: <message>`; messages from the desktop (validation, "still in
use", …) are shown verbatim. Exit code `3` prints `NEEDS TTY: <explanation>` instead.

## Resources and references

| Resource | Located by | `list` filters |
| --- | --- | --- |
| `agent` | id or name (unique) | `--department <d>` |
| `department` / `dept` | id, name, or `parent/child` path | — |
| `project` / `proj` | id, name, or `parent/child` path | `--parent <p>` |
| `provider` | id or name (unique) | — |
| `model` | id or `<provider>/<model id>` (split at the first `/`) | `--provider <p>` |
| `backend` | id or name (unique) | `--type <type>` |
| `memory` | its id: `m58` or `58` | `--layer <layer>`, `--query <text>` |

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

Empty cells print `-`. `-o json` prints a JSON array of the objects `get` prints, except that a model's `key` is
shown by `get` only.

## `get`

```
{{AGRCTL_PATH}} get provider openrouter
{
  "id": 4,
  "name": "openrouter",
  "type": "openai-chat",
  "baseUrl": "<url>",
  "enabled": true,
  "apiKeySet": true,
  "defaultModel": "openai/gpt-5.1",
  "models": [ { "modelId": "openai/gpt-5.1", "contextWindow": 400000, "enabled": true } ],
  "references": { "agentBackends": 2 }
}
```

Field names match the create/update flags. References to other resources are printed as
paths you can pass straight back to a flag. API keys are never printed — `apiKeySet` says
whether one is set; other secrets only print
`set` / `unset`, or `unknown` when the device that keeps them cannot be asked (it is offline,
or the backend is managed through the server). `list backends` shows the same state in its
last column: `set`, `-` or `unknown`.

## `create`, `update`, `delete`

```
{{AGRCTL_PATH}} create department --name 研发部 --parent 总部 --lead architect
department 研发部 created (id 5)

{{AGRCTL_PATH}} update agent reviewer --department 质量部
waiting for approval in session #231 …
agent reviewer updated

{{AGRCTL_PATH}} create model --provider openrouter --model-id qwen/qwen3-coder
model openrouter/qwen/qwen3-coder created (id 31)

{{AGRCTL_PATH}} delete department 临时小组 --cascade
department 临时小组 deleted
```

- `update` sends only the flags given; everything else stays as it is.
- An API key cannot be emptied: `--api-key=""` is a usage error (exit `2`). Omit the flag to
  keep the current key.
- Flags that set the same field cannot be combined (`--enable` with `--disable`,
  `--config` with `--config-file`).
- `--config '<JSON>'` must be a JSON object whose keys are all listed by
  `help backend <type>`; `update` replaces only the keys it contains.
- `delete department` moves sub-departments and agents up to the parent unless `--cascade`;
  `delete provider` / `delete model` need `--force` while a backend still uses them.
  Other refusals (a project with sub-projects or active sessions, the system agent) come
  back from the desktop verbatim.

From an Agentre session every write first prints `waiting for approval in session #<id> …`
on stderr and blocks until the user answers the approval card in that session. Outside any
session, a program without a terminal — or one run by an agent CLI (Claude Code, Codex, Gemini
CLI, opencode, …), even inside a terminal — prints `waiting for approval in the Agentre desktop …`
instead and blocks until the user answers the desktop's approval dialog. A rejection:

```
Error: rejected in session #231
```

## Memory

Inside an Agentre session reads see this session's memories only and writes never wait for
approval. Outside a session the usual approval rules apply (a person at a terminal writes
directly, any other caller waits for the desktop), every layer is visible, and `create` needs a
named layer: `--layer global`, `--department <d>`, `--agent <a>` or `--project <p>`.

```
{{AGRCTL_PATH}} list memory --query lint
ID    LAYER            SUMMARY                          DETAIL   CARRIED   UPDATED
m58   agent reviewer   Run lint before committing       yes      yes       2026-11-02
m61   agent reviewer   Prod database is older than dev  -        no        2026-11-04

{{AGRCTL_PATH}} create memory --summary "Release builds pull from the internal registry" --layer project
memory m83 created (project harbor)

{{AGRCTL_PATH}} create memory --summary "Run lint before committing"
memory m58 already exists (agent reviewer)

{{AGRCTL_PATH}} update memory m58 --detail "CI only runs tests"
memory m58 updated (agent reviewer)
over budget: agent reviewer is 120 characters over its memory budget; merge or shorten the memories there

{{AGRCTL_PATH}} delete memory m58
memory m58 archived (agent reviewer)
```

- The first result line is always `memory m<id> <created|already exists|updated|archived> (<layer>)`,
  where `<layer>` is `global`, `department <path>`, `agent <name>` or `project <path>`.
  The `over budget:` line follows only when the write leaves that layer over its budget.
- `CARRIED no` means the memory did not fit its layer's budget and was not carried into this
  session; `get` still shows it in full.
- `get memory <id>` prints `id`, `layer`, `summary`, `detail`, `pinned`, `carried`, `updated`.
- A layer that is not yours, a memory this session cannot see (`memory "m500" not found`) and
  `--layer department` for an agent in no department are refused with the desktop's message.

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
{{AGRCTL_PATH}} send --agent <name> [--agent-id <id>] [--device <device> | --backend <ref>] [--project <ref>] [--title <title>] [--isolated]
                    [--model <provider>/<model> | --cli-model <model>] [--effort <level>]
                    [--permission-mode <mode>] [--image <file>]... [--wait [--timeout <duration>]] [-o json] <task text...>
{{AGRCTL_PATH}} send --session <id> [--steer] [--image <file>]... [--wait [--timeout <duration>]] [-o json] <text...>
```

The task text is everything after the flags; quote it so the shell keeps it as one argument
when it contains spaces.

- Fire and forget (preferred for anything long):

  ```
  {{AGRCTL_PATH}} send --agent reviewer --project 3 "review the diff on branch feat/x"
  ```

  Stdout is the session id — a full UUID such as `0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b`;
  stderr carries `dispatched to reviewer — this desktop` (or the device that runs it).

- Wait for the answer (only for a short question):

  ```
  {{AGRCTL_PATH}} send --agent reviewer --wait "which files did you touch last?"
  ```

  Stdout is that agent's final text once the session is idle; the exit codes are those of
  `wait` below.

- One-shot session that stays out of the sidebar:

  ```
  {{AGRCTL_PATH}} send --agent-id 12 --isolated "summarize the release notes"
  ```

  Against the account (after `agrctl login`, outside an Agentre session) `send`, `wait` and
  `stop` reach the device running the session directly; a session is saved to the account
  (the console and the desktop list it) unless `--isolated`, which keeps it out of the account.
  A device on another release fails naming the older end to upgrade — `device <name> needs to be
  upgraded …` or `this agrctl needs to be upgraded to reach device <name> …` (exit `1`).

- Continue a session, or interject into its running turn:

  ```
  {{AGRCTL_PATH}} send --session 0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b "now add the tests"
  Error: session is running; use --steer to interject, or agrctl wait first
  {{AGRCTL_PATH}} send --session 0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b --steer "also cover the empty case"
  ```

- Pick where a new session runs (default: the agent's first available backend):

  ```
  {{AGRCTL_PATH}} send --agent reviewer --device build-box "run the slow suite"
  {{AGRCTL_PATH}} send --agent reviewer --device nowhere "…"
  Error: --device "nowhere" is not one of agent reviewer's devices (one of: this desktop, build-box)
  ```

  `--device` takes a device name or fingerprint (`local` is this desktop) and picks the agent's
  first backend there; `--backend` names one of the agent's backends exactly. A device or
  backend the agent does not have is a usage error (exit `2`); an offline device, or one with no
  path for the project, fails with the reason (exit `1`).

- Change a setting for this session (from the next turn on):

  ```
  {{AGRCTL_PATH}} send --agent reviewer --effort turbo "…"
  Error: --effort "turbo" is not supported by the target backend (one of: low, medium, high, xhigh, max)
  ```

- `-o json` prints one object: `{"session": "<id>", "status": "running|idle|failed|waiting", "text": …, "error": …, "pending": …}`.

Reminders: `--wait` blocks until the session settles, dispatching to the agent this session is
running as deadlocks both sides, and `send --session` to your own session is refused (exit `2`).
Foreground shell tools time out (Claude Code's foreground Bash stops after 10 minutes), so for a
long turn dispatch without `--wait` and run `wait <session>` as a background shell command.

## `wait`

```
{{AGRCTL_PATH}} wait <session> [--timeout <duration>] [-o json]
```

| Session state | stdout | exit |
| --- | --- | --- |
| idle | final text of the last turn | `0` |
| the last turn failed | — (stderr: its error) | `1` |
| waiting for input | the pending items as JSON | `4` |
| `--timeout` passed | — (stderr: `still running …`) | `5` |

A dropped connection to the executor is retried and the wait continues. No timeout by default.

## `tail`

```
{{AGRCTL_PATH}} tail <session> [--from <cursor>] [--until-idle]
{"cursor":"v1:12","type":"tool_call","kind":"tool_use_start","data":{…}}
{"cursor":"v1:12","type":"text","kind":"text_delta","preview":true,"data":{"kind":"text_delta","text":"Done"}}
{"cursor":"v1:14","type":"turn_done","kind":"runtime.runResultDone","data":{…}}
{"cursor":"v1:14","type":"status","status":"idle"}
```

One JSON object per line. `type` is one of `turn_started`, `text`, `thinking`, `tool_call`,
`tool_result`, `user_message`, `pending`, `pending_resolved`, `turn_done`, `status`, `event`
(anything else); `data` is the event as the executor saw it. `preview: true` lines are live
increments — the settled content follows as a line without it. Treat `cursor` as opaque:
`--from <cursor>` replays what came after it, then follows live. An expired cursor fails
(exit `1`): read the history with `agrctl messages`. `--until-idle` exits `0` when the session
goes idle and `4` when it waits for input; without it, `tail` follows until Ctrl-C.

## `stop`

```
{{AGRCTL_PATH}} stop <session>
stopped — dropped 1 undelivered steer message(s)
```

A session that is not running prints `session is not running` (exit `0`). Stopping is refused
from inside an Agentre session.

## `list sessions`, `get session`

```
{{AGRCTL_PATH}} list sessions [--agent <name|id>] [--project <ref>] [--device <device>]
                     [--status running|idle|waiting|failed] [--limit <n>] [-o json]
{{AGRCTL_PATH}} get session <session>
```

`list sessions` shows this desktop's sessions (including the ones it runs on remote devices),
most recently active first; the first column is the full session id. `--device local` means this
desktop; `--limit` defaults to 50. `--mine` is refused on the desktop (exit `2`). `-o json` prints
`[{id, title, agent, device, status, lastActivityAt}]`.

`get session` prints one JSON object: `id`, `title`, `agent`, `project`, `device`, `backend`,
`model`, `status`, `initiator` (who started the session) with `initiatorKind` (`desktop`,
`console`, `cli`, ...), `createdAt`, `lastActivityAt`, `usage {input, output}`, `pending` (how many items
await an answer) and `lastText` (the last turn's final text, shortened). A field that cannot be
determined is omitted, not guessed. On the desktop `initiator` is `desktop` (it does not record
which agrctl started a session). Against the account it is `this agrctl (<device>)` for
your own sessions, another device's name, or `console`; when the initiator cannot be named `initiator` is
left out, and `initiatorKind` too unless its kind is known.

Against the account (after `agrctl login`) the same commands work on the account's sessions:
`list sessions` merges the sessions saved to the account (console and CLI started) with the ones
running on online devices; a saved session on an offline device keeps its last known status and
is marked (`deviceOffline: true`, `(offline)` in the table), and unsaved sessions of an offline
device are absent. `--mine` keeps only the sessions this caller started. `messages` falls back to
the account's mirror when the device is offline (`device … is offline` when nothing was mirrored),
and `tail` needs the device online.

## `messages`

```
{{AGRCTL_PATH}} messages <session> [--last <n>] [--before <cursor>] [--full] [-o json]
```

Prints the last 20 turns by default, oldest first: user messages and the assistant's text
(`user: …` / `assistant: …`). `--full` adds tool calls and results (thinking is never shown).
When earlier messages exist, stderr prints the next command: `messages <session> --before <cursor>`.
`-o json` prints `{session, messages: [{role, at, text, blocks}], hasMore, before}`.

## `pending`, `answer`

```
{{AGRCTL_PATH}} pending [<session>] [-o json]
{{AGRCTL_PATH}} answer <session> <id> --option 2
{{AGRCTL_PATH}} answer <session> <id> --text "use sqlite"
{{AGRCTL_PATH}} answer <session> <id> --allow
{{AGRCTL_PATH}} answer <session> <id> --deny --message "not in this repo"
```

`pending` lists what a session waits for: `kind` `question` (an AskUserQuestion card, with its
options), `tool_permission` (a tool, plan or execution approval) or `approval` (the card a session's
agent raised by writing with agrctl, with `title` and `summary`). Without a session it covers every
waiting session. `-o json` prints `{items: [{id, session, kind, tool, input, questions, title, summary}]}`.

`answer` takes the item `id`. A question takes `--option` (its text or its 1-based number; repeat for
multi-select) or `--text`; a tool permission takes `--allow` or `--deny [--message <reason>]`, an
approval card `--allow` or `--deny`.
The wrong kind of answer for an item is a usage error (exit `2`). An item already answered
elsewhere, or timed out, fails with "the pending item no longer exists" (exit `1`). A card that
asks several questions at once is answered in the desktop app. `answer` is refused from inside an
Agentre session (exit `1`).

## `test provider`, `discover models`, `import models`

```
{{AGRCTL_PATH}} test provider <provider> [--model <model id>] [--device <device>]
{{AGRCTL_PATH}} discover models <provider> [--device <device>] [-o json]
{{AGRCTL_PATH}} import models <provider> (--all | <model id>...) [--device <device>]
```

`test provider` sends one real request with the saved configuration and prints success, or the
upstream error summary (exit `1`; never the API key). `discover models` lists the models the
upstream offers and marks the ones already imported (`-o json`: `modelId`, `name`, `imported`).
`import models` creates the missing ones as models of the provider through the same write path
as `create model` (so the same approval rules apply) and skips existing ones, printing both
counts. Against the account these run on `--device` (default: the first online device that
already has the provider); `--device` is not available against the desktop, which runs them
itself. A provider created or changed a moment ago may not have reached the device yet:
`device <name> has not received this provider yet — try again shortly` (exit `1`); wait and retry.

## `list devices`, `get device`, `update device`, `delete device`, `upgrade device`

```
{{AGRCTL_PATH}} list devices [-o json]
{{AGRCTL_PATH}} get device <device>
{{AGRCTL_PATH}} update device <device> --name <name>
{{AGRCTL_PATH}} delete device <device>
{{AGRCTL_PATH}} upgrade device <device>
```

Account only: against the desktop or a session these fail with `device management needs an
account login — run 'agrctl login'` (exit `1`). `list devices` shows name, kind, online state (a
`cli` device shows when it was last used), version and a `*` on the device this `agrctl` is
logged in as. `get device` also lists the backends bound to it. `update`, `delete` and `upgrade`
change the account, so a program using the user's login is refused like any other write;
`delete device` on the current device is the same as `logout`; only an `agentred` device can be
upgraded. Pairing a new `agentred` is done on that machine with `agentred login`.

## `login`, `logout`, `whoami`

```
{{AGRCTL_PATH}} login [--server <url>]
{{AGRCTL_PATH}} logout
{{AGRCTL_PATH}} whoami
```

`login` and `logout` are the user's, run in their own terminal. `login` prints a code to approve
in the browser and makes this machine's `agrctl` a device of the account; it fails (exit `1`)
when denied, expired, unreachable, or already logged in (`already logged in as <account> — run
'agrctl logout' first`). `logout` revokes that device, then deletes the local login; if the
server cannot be reached it still deletes it and says to remove the device from the account's
device list. `whoami` prints the executor the next command reaches (`session`, `desktop` or
`account`) and the login's server, account and device.

With the user's login, a program without a terminal or one run by an agent CLI can read, but
writes and sending, stopping or answering fail (exit `1`) with a message to use a personal
credential instead. `memory` and a project's local `--path` are not available on the account;
`--desktop` reaches the desktop.

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
