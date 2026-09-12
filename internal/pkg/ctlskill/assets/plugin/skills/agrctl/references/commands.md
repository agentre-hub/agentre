# agrctl ctl — command reference

Every command below is invoked by absolute path:

```
{{AGRCTL_PATH}} ctl <command> [flags]
```

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | the command succeeded |
| `1` | the desktop could not be reached, or it rejected the request |
| `2` | usage error — unknown subcommand, missing `--agent`/`--agent-id`, or missing task text |

Failures print a single line on stderr prefixed with `ctl:`.

## `ctl agents`

```
{{AGRCTL_PATH}} ctl agents
```

Stdout, one line per agent, tab separated:

```
#12	reviewer	reviews diffs before they land	[system]
```

The description and the `[badge]` column are omitted when empty.

## `ctl projects`

```
{{AGRCTL_PATH}} ctl projects
```

Stdout, one line per project, tab separated:

```
#3	agentre	/Users/me/Code/agentre
```

## `ctl send`

```
{{AGRCTL_PATH}} ctl send --agent <name> [--agent-id <id>] [--project <id>] [--wait] [--isolated] <task text...>
```

The task text is everything after the flags; quote it so the shell keeps it as one argument
when it contains spaces.

- Fire and forget (preferred for anything long):

  ```
  {{AGRCTL_PATH}} ctl send --agent reviewer --project 3 "review the diff on branch feat/x"
  ```

  Stdout is the new session id; stderr carries a `dispatched to …` line.

- Wait for the answer (only for a short question):

  ```
  {{AGRCTL_PATH}} ctl send --agent reviewer --wait "which files did you touch last?"
  ```

  Stdout is that agent's final text, once its turn ends.

- One-shot session that stays out of the sidebar:

  ```
  {{AGRCTL_PATH}} ctl send --agent-id 12 --isolated "summarize the release notes"
  ```

Reminders: `--wait` blocks for the whole remote turn, and dispatching to the agent this
session is running as deadlocks both sides.
