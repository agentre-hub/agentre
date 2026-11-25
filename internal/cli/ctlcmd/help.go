package ctlcmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

const usageText = `agrctl — Agentre companion CLI: manage and drive Agentre — the desktop on this machine or your account

Usage:
  agrctl list <resource> [filters] [-o json]    list resources as a table (first column: ID)
  agrctl get <resource> <ref>                   print one resource as JSON
  agrctl create <resource> [flags]              create a resource
  agrctl update <resource> <ref> [flags]        change only the flags given
  agrctl delete <resource> <ref> [flags]        delete a resource
  agrctl help [<resource> [<backend type>]]     flags, --config fields, secrets and examples
  agrctl send --agent <name> [flags] <text...>  dispatch a task to an agent (prints the session id)
  agrctl send --session <id> [flags] <text...>  continue a session (--steer interjects into a running turn)
  agrctl wait <session> [--timeout <d>]         wait until the session is idle or waits for input
  agrctl tail <session> [--from <cursor>]       follow the session's events as NDJSON
  agrctl stop <session>                         stop the running turn, dropping undelivered steers
  agrctl list sessions [filters] [-o json]      sessions on this desktop (first column: session id)
  agrctl get session <session>                  one session as JSON (unknown fields are omitted)
  agrctl messages <session> [--last n] [--full] the last turns of a session (--before <cursor> pages back)
  agrctl pending [<session>] [-o json]          questions and tool permissions waiting for an answer
  agrctl answer <session> <id> --option … | --text … | --allow | --deny   answer one pending item
  agrctl acp --agent <name>                     run as an ACP v1 agent over stdio
  agrctl login [--server <url>]                 log this machine's agrctl into an account (opens a device login)
  agrctl logout                                 revoke this agrctl device on the server and delete its login
  agrctl whoami                                 server, account, device, and which executor commands reach

Resources: agent, department (dept), project (proj), provider, model, backend, memory.
<ref> is a numeric id, a name, or a parent/child path (projects, departments);
a model is <provider>/<model id>; a memory is its id (m58 or 58). Flags that name
another resource (--department, --parent, --backend, --provider, --lead, …) take
the same forms. A <session> is always the full session UUID that agrctl send prints.

Executor (agrctl whoami shows it): inside an Agentre session, that session; with
--desktop (accepted by every command), the Agentre desktop on this machine — "not
running" (exit 1) if it is not; with a personal token in AGENTRE_TOKEN (server:
AGENTRE_SERVER_URL) or after agrctl login, the account on the server; otherwise the
desktop on this machine. On the account a person at the terminal reads and writes
directly. A program without a terminal, or one an agent CLI runs, may only read with
agrctl's login: writes and sending, stopping or answering are refused — issue it a
personal token instead. memory exists only in the desktop and inside sessions.

Writes from an Agentre session wait for approval in that session; on the desktop,
writes from a program without a terminal, or from a terminal an agent CLI (Claude Code, Codex,
Gemini CLI, opencode, …) is running in, wait for approval in the desktop.
memory is the exception inside an Agentre session: its writes take effect at
once (agrctl help memory). Inside an Agentre session agrctl can read, wait for,
follow and continue other sessions, but cannot send to its own session, stop one or answer pending items.

Exit codes: 0 ok · 1 failed, rejected, session running or executor unreachable ·
2 usage error (unknown command or flag, missing value, ambiguous ref, unsupported
value) · 3 needs a human (a secret must be typed in a terminal: NEEDS TTY) ·
4 waiting for input (pending items on stdout) · 5 wait timed out (still running).`

// shellTimeoutNote 是长时间等待的 shell 超时提醒(send --wait 与 wait 共用)。
const shellTimeoutNote = `Waiting holds the shell command open until the session settles. Shell tools time out (Claude Code's
foreground Bash stops after 10 minutes), so for a long task dispatch it without --wait and run
agrctl wait as a background shell command (or give --timeout and wait again on exit 5).`

func sendUsage() string {
	var b strings.Builder
	b.WriteString(`agrctl send --agent <name|id> [--device <device> | --backend <ref>] [--project <ref>] [--title <title>] [--isolated]
            [--model <provider>/<model> | --cli-model <model>] [--effort <level>]
            [--permission-mode <mode>] [--image <file>]... [--wait [--timeout <duration>]] [-o json] <text...>
agrctl send --session <id> [--steer] [--image <file>]... [--model …] [--effort …]
            [--wait [--timeout <duration>]] [-o json] <text...>

`)
	writeFlagTable(&b, withoutOutputFlags(sendFlags), nil)
	b.WriteString(`  -o json                    print one JSON object: session, status, and text / error / pending

Without --wait it prints the session id on stdout ("dispatched to <agent> — <device>" on stderr)
and returns at once; the turn keeps running. With --wait it behaves like agrctl wait.
A running session refuses a plain send (exit 1): add --steer to interject into the current turn —
steers not yet delivered when the turn ends go out, in order, at the start of the next one.
Without --device / --backend a new session runs on the agent's first available backend, in the
agent's order. A --device or --backend that is not one of the agent's fails with the choices
listed (exit 2); one that is offline, or has no path for the project, fails with the reason (exit 1).
An unsupported --model / --cli-model / --effort / --permission-mode value fails with the
choices listed (exit 2); the values apply to this session from the next turn on.
Against the account the session runs on the chosen device and is saved to the account (the
console and the desktop see it) unless --isolated; a device that is offline, or on another
release ("device <name> needs to be upgraded"), fails with exit 1.

`)
	b.WriteString(shellTimeoutNote)
	return b.String()
}

const waitUsage = `agrctl wait <session> [--timeout <duration>] [-o json]

Waits until the session settles:
  idle                the final text of the last turn on stdout — exit 0 (a failed turn: its error, exit 1)
  waiting for input   the pending items as JSON on stdout — exit 4
  --timeout elapsed   "still running" — exit 5; the session keeps running, wait again
Reconnects by itself if the connection to the executor drops.

` + shellTimeoutNote

const tailUsage = `agrctl tail <session> [--from <cursor>] [--until-idle]

Prints the session's events as NDJSON, one per line: turn start, text deltas, tool calls,
tool results, pending items appearing and resolving, turn end and status changes. Every
line carries an opaque "cursor"; --from <cursor> resumes after it (missed events first,
then live ones). Lines with "preview": true are live increments; the settled content
follows as a line without it. An expired cursor fails — read the history with
agrctl messages instead.
  --until-idle   exit when the session goes idle (exit 0) or waits for input (exit 4)
Follows until Ctrl-C otherwise; reconnects from the last cursor if the connection drops.`

const stopUsage = `agrctl stop <session> [-o json]

Stops the running turn and drops the steer messages not yet delivered, printing how many
were dropped. A session that is not running is left alone ("session is not running", exit 0).
An Agentre session cannot stop sessions.`

const listSessionsUsage = `agrctl list sessions [--agent <name|id>] [--project <ref>] [--device <device>]
                     [--status running|idle|waiting|failed] [--mine] [--limit <n>] [-o json]

Lists the sessions of this desktop (including the ones it runs on remote devices), most recently
active first. The first column is the full session id every other session command takes.
--mine needs an account and is refused on the desktop (exit 2). Against the account it merges the
saved sessions with the ones running on online devices (an offline device's saved sessions keep
their last known status and are marked offline). -o json prints an array of
{id, title, agent, device, status, lastActivityAt}.`

const getSessionUsage = `agrctl get session <session>

Prints one session as JSON: id, title, agent, project, device, backend, model, status, createdAt,
lastActivityAt, usage {input, output}, pending (count of unanswered items) and lastText (the
last turn's final text, shortened). A field that cannot be determined is left out, never guessed.`

func sessionResourceUsage(verb string) string {
	if verb == "get" {
		return getSessionUsage
	}
	return listSessionsUsage
}

var messagesUsageFlags = withoutOutputFlags(messagesFlags)

func messagesUsage() string {
	var b strings.Builder
	b.WriteString("agrctl messages <session> [--last <n>] [--before <cursor>] [--full] [-o json]\n\n")
	writeFlagTable(&b, messagesUsageFlags, nil)
	b.WriteString(`  -o json                    print {session, messages, hasMore, before}

Prints the last 20 turns by default, oldest first: user messages and the assistant's text. --full also
prints the tool calls and results (thinking is never shown). When earlier messages exist, stderr
names the cursor: agrctl messages <session> --before <cursor> pages back.`)
	return b.String()
}

const pendingUsage = `agrctl pending [<session>] [-o json]

Lists what is waiting for an answer: kind question (an AskUserQuestion card, with its options) or
tool_permission (a tool, plan or execution approval). Without a session it covers every session
that is waiting. -o json prints {items: [{id, session, kind, tool, input, questions}]}.
Answer with agrctl answer. An Agentre session can read pending items but not answer them.`

const answerUsage = `agrctl answer <session> <id> --option <text|number> [--option …] | --text <answer>
agrctl answer <session> <id> --allow | --deny [--message <reason>]

Answers one pending item (the id from agrctl pending). A question takes --option (its text, or its
1-based number; repeat for multi-select) or --text. A tool permission takes --allow or --deny, and
--deny may carry the reason handed back to the agent. An item that was already answered elsewhere
or timed out fails with "the pending item no longer exists" (exit 1). An Agentre session cannot answer.`

// withoutOutputFlags 去掉 -o/--output(help 里单独写成 -o json)。
func withoutOutputFlags(defs []*flagDef) []*flagDef {
	out := make([]*flagDef, 0, len(defs))
	for _, d := range defs {
		if d != outputFlag && d != outputFlagLong {
			out = append(out, d)
		}
	}
	return out
}

// verbUsage 是会话命令的帮助;不是会话命令时为空串。
func verbUsage(verb string) string {
	switch verb {
	case "send":
		return sendUsage()
	case "wait":
		return waitUsage
	case "tail":
		return tailUsage
	case "stop":
		return stopUsage
	case "messages":
		return messagesUsage()
	case "pending":
		return pendingUsage
	case "answer":
		return answerUsage
	case "login":
		return loginUsage
	case "logout":
		return logoutUsage
	case "whoami":
		return whoamiUsage
	}
	return ""
}

const loginUsage = `agrctl login [--server <url>]

Logs this machine's agrctl into an account as a device of kind cli: prints a URL and a code,
then waits until the code is approved in the browser. --server defaults to AGENTRE_SERVER_URL,
else ` + ctlclient.DefaultServerURL + `. The login is kept in this build channel's
data directory (file mode 0600) and refreshed by itself. Denied, expired or unreachable:
exit 1. Already logged in: exit 1 — run agrctl logout first. When a command says the login
expired or the device was revoked, run agrctl login again.`

const logoutUsage = `agrctl logout

Revokes this agrctl device on the server, then deletes the local login. If the server cannot be
reached the local login is deleted anyway; remove the device from the account's device list.`

const whoamiUsage = `agrctl whoami

Prints the executor the next command would reach (session, desktop or account), and the server,
account and device of agrctl's login. It never prints a credential.`

// wantsHelp 报告参数里是否有 -h / --help。
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
		if a == "--" {
			return false
		}
	}
	return false
}

// runHelp：`help [资源 [后端类型]]`。纯客户端，不连接任何执行者。
func runHelp(args []string, w io.Writer) error {
	switch len(args) {
	case 0:
		_, _ = fmt.Fprintln(w, usageText)
		return nil
	case 1, 2:
	default:
		return usageErrorf("help takes at most a resource and a backend type")
	}
	if u := verbUsage(args[0]); u != "" && len(args) == 1 {
		_, _ = fmt.Fprintln(w, u)
		return nil
	}
	if isSessionResource(args[0]) && len(args) == 1 {
		_, _ = fmt.Fprintln(w, listSessionsUsage+"\n\n"+getSessionUsage)
		return nil
	}
	spec := lookupKind(args[0])
	if spec == nil {
		return usageErrorf("unknown resource %q (one of: %s)", args[0], kindNames())
	}
	if len(args) == 2 {
		if spec != kindBackend {
			return usageErrorf("only backend has per-type help")
		}
		t := lookupBackendType(args[1])
		if t == nil {
			return usageErrorf("unknown backend type %q (one of: %s)", args[1], backendTypeNames())
		}
		writeBackendTypeHelp(w, t)
		return nil
	}
	writeKindHelp(w, spec)
	return nil
}

func writeKindHelp(w io.Writer, spec *kindSpec) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Resource: %s\n\nCommands:\n", spec.name)
	ref := "<ref>"
	switch spec {
	case kindModel:
		ref = "<provider>/<model id>"
	case kindMemory:
		ref = "<id>"
	}
	filters := ""
	for _, f := range spec.filters {
		filters += " [--" + f.name + " " + f.value + "]"
	}
	_, _ = fmt.Fprintf(&b, "  agrctl list %s%s [-o json]\n", spec.plural, filters)
	_, _ = fmt.Fprintf(&b, "  agrctl get %s %s\n", spec.name, ref)
	req := ""
	for _, r := range spec.required {
		if d := findDef(spec.createFlags, r); d != nil {
			req += " --" + r + " " + d.value
		}
	}
	_, _ = fmt.Fprintf(&b, "  agrctl create %s%s [flags]\n", spec.name, req)
	_, _ = fmt.Fprintf(&b, "  agrctl update %s %s [flags]    (only the flags given change)\n", spec.name, ref)
	del := ""
	for _, f := range spec.deleteFlags {
		del += " [--" + f.name + "]"
	}
	if spec == kindMemory {
		del += "    (archives it)"
	}
	_, _ = fmt.Fprintf(&b, "  agrctl delete %s %s%s\n", spec.name, ref, del)

	b.WriteString("\nFlags (create / update):\n")
	writeFlagTable(&b, mergeFlags(spec.createFlags, spec.updateFlags), func(d *flagDef) string {
		inCreate, inUpdate := findDef(spec.createFlags, d.name) != nil, findDef(spec.updateFlags, d.name) != nil
		switch {
		case inCreate && !inUpdate && !strings.Contains(d.usage, "create only"):
			return " (create only)"
		case inUpdate && !inCreate:
			return " (update only)"
		}
		return ""
	})
	if len(spec.deleteFlags) > 0 {
		b.WriteString("\nFlags (delete):\n")
		writeFlagTable(&b, spec.deleteFlags, nil)
	}
	if len(spec.filters) > 0 {
		b.WriteString("\nFilters (list):\n")
		writeFlagTable(&b, spec.filters, nil)
	}
	if spec == kindBackend {
		_, _ = fmt.Fprintf(&b, "\nTypes: %s\n  --config fields depend on the type: agrctl help backend <type>\n", backendTypeNames())
	}
	if spec.notes != "" {
		b.WriteString("\n" + spec.notes + "\n")
	}
	b.WriteString("\nSecrets:\n")
	if spec.secretHelp != "" {
		b.WriteString("  " + strings.ReplaceAll(spec.secretHelp, "\n", "\n  ") + "\n")
	} else if spec == kindBackend {
		b.WriteString("  --token (openclaw only), see agrctl help backend openclaw\n")
	} else {
		b.WriteString("  none\n")
	}
	b.WriteString("\nExamples:\n")
	for _, e := range spec.examples {
		b.WriteString("  " + e + "\n")
	}
	_, _ = io.WriteString(w, b.String())
}

// mergeFlags 合并 create 与 update 的 flag，保持首次出现的顺序。
func mergeFlags(a, b []*flagDef) []*flagDef {
	out := append([]*flagDef{}, a...)
	for _, d := range b {
		if findDef(out, d.name) == nil {
			out = append(out, d)
		}
	}
	return out
}

// writeFlagTable 写一张 flag 表；note 可为 nil，否则给每个 flag 追加一段说明。
func writeFlagTable(b *strings.Builder, defs []*flagDef, note func(*flagDef) string) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, d := range defs {
		name := "--" + d.name
		switch {
		case d.secret:
			name += "[=<value>]"
		case d.value != "":
			name += " " + d.value
		}
		usage := d.usage
		if note != nil {
			usage += note(d)
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", name, usage)
	}
	_ = tw.Flush()
}

func writeBackendTypeHelp(w io.Writer, t *backendType) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Backend type: %s — %s\n\n", t.name, t.summary)
	b.WriteString("Flags: see agrctl help backend (--name, --device, --provider, --model,\n" +
		"       --reasoning-effort, --env KEY=VAL, --config, --config-file)\n\n")
	b.WriteString("--config fields:\n")
	if len(t.config) == 0 {
		b.WriteString("  none (any --config key is rejected)\n")
	} else {
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, f := range t.config {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\n", f.key, f.values)
		}
		_ = tw.Flush()
	}
	b.WriteString("\nSecrets:\n")
	if t.token {
		b.WriteString("  --token        bare: typed in without echo (needs a terminal).\n" +
			"                 --token=<value> also works, but the token stays in your shell history.\n" +
			"  get / list show only its state: set / unset / unknown. unknown means the device the\n" +
			"  backend is bound to cannot be asked right now (offline, or managed through the server).\n")
	} else {
		b.WriteString("  none\n")
	}
	b.WriteString("\nExample:\n")
	switch t.name {
	case "openclaw":
		b.WriteString(`  agrctl create backend --type openclaw --name claw --config '{"openclawGatewayUrl":"ws://127.0.0.1:18789","openclawSessionMode":"per-agentre-session"}' --token` + "\n")
	case "builtin":
		b.WriteString("  agrctl create backend --type builtin --name my-builtin --provider anthropic\n")
	case "piagent":
		b.WriteString("  agrctl create backend --type piagent --name my-piagent --provider openrouter --model openai/gpt-5.1\n")
	case "antigravity":
		b.WriteString("  agrctl create backend --type antigravity --name my-agy --provider huu\n")
	case "codex":
		b.WriteString(`  agrctl create backend --type codex --name codex-remote --device build-box --config '{"sandbox":"workspace-write","approval":"on-request"}'` + "\n")
	default:
		_, _ = fmt.Fprintf(&b, "  agrctl create backend --type %s --name my-%s\n", t.name, t.name)
	}
	_, _ = io.WriteString(w, b.String())
}
