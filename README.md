<p align="right">
<a href="./README.md">English</a> | <a href="./docs/README_zh.md">中文</a>
</p>

<h1 align="center">
<img src="build/appicon.png" width="128" height="128"/><br/>
Agentre
</h1>

<p align="center">A local-first desktop workspace for coordinating Claude Code, Codex, and other AI coding agents across projects, sessions, and remote machines.</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  &nbsp;
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=white" alt="React">
  &nbsp;
  <img src="https://img.shields.io/badge/Wails-v2-EB4034?style=for-the-badge&logo=wails&logoColor=white" alt="Wails">
  &nbsp;
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey?style=for-the-badge&logo=windows&logoColor=white" alt="Platform">
</p>

## About

AI coding work rarely fits inside one terminal tab anymore. A real workday may have one agent fixing backend behavior, another reviewing a frontend change, a third investigating a production log, and a fourth waiting for tool approval.

Agentre gives that workflow one desktop home. Each **Agent** has a role, avatar, system prompt, skills, and backend engine. Agents live inside **Departments**, work inside **Projects**, and run many **Sessions** in parallel. The app is built for quickly seeing what is running, what is waiting, and where to jump next.

**If you find it useful, please give us a Star ⭐ — it means a lot!**

## What You Can Do

- **Run multiple coding agents side by side**: keep frontend, backend, reviewer, release, or ops agents active at the same time without losing their individual context.
- **Choose the engine per agent**: assign Claude Code, Codex, or a built-in engine to each agent while keeping the same chat workflow.
- **Organize work by project**: group sessions, members, and agents around a codebase or initiative so recent work is easy to scan.
- **Switch fast with the command palette**: use `⌘K` to open chats, jump between sessions, change projects, and trigger agent actions from the keyboard.
- **Review tool activity in place**: file edits, shell commands, MCP calls, permission prompts, and ask-user questions appear as explicit in-app cards.
- **Run sessions on another machine**: pair a LAN `agentred` daemon and run agent work on a remote dev box while approvals stay in the desktop app.
- **Keep long tasks manageable**: queued messages, active-session indicators, abort/resume controls, and waiting states make parallel work easier to supervise.

## App Surfaces

| Area | What it is for |
| ---- | -------------- |
| **Chat** | Drive agent sessions, inspect tool calls, approve actions, queue follow-up messages, and resume interrupted work. |
| **Board** | Track tasks across four columns, narrow the board to a project subtree, filter and edit in place; tasks record which agent, machine and model they are meant for. |
| **Org** | Model agents as departments and sub-departments, with leads, colors, and role-specific profiles. |
| **Hooks** | Route external triggers such as webhooks, notifications, timers, or messages into agent workflows. |
| **Settings** | Configure agent backends, remote devices, project membership, and session permission modes. |

## Core Concepts

| Concept | What it means |
| ------- | ------------- |
| **Agent** | A role + avatar + system prompt + skills + backend engine. An agent can run many sessions. |
| **Department** | A nested organizational container for agents, with an optional lead and theme color. |
| **Session** | One conversation or task run, with states such as `running`, `waiting`, and `idle`. |
| **Project** | A workspace scope for a codebase or initiative, bundling members, agents, and sessions. |
| **Task** | A card on the board: one of four columns, optionally under a project, with labels and an intended agent, machine and model. |
| **Hook** | An external event source that can dispatch work to agents through routing rules. |

## Remote Execution

Agentre uses `agentred`, a companion daemon for running sessions on another macOS, Linux, or Windows machine on your LAN. Released builds are available for amd64 and arm64.

Install the latest release on macOS or Linux:

```bash
curl -fsSL https://github.com/agentre-hub/agentre/releases/latest/download/install.sh | sh
agentred --version
agentred service install --start
agentred service status
```

Install the latest release from PowerShell on Windows (no administrator shell required):

```powershell
irm https://github.com/agentre-hub/agentre/releases/latest/download/install.ps1 | iex
agentred --version
agentred service install --start
agentred service status
```

Then connect it to the desktop app:

1. Open **Settings → Remote devices** and pair the `agentred` daemon.
2. Open **Settings → Agent backends** and create a backend whose run device is the paired machine.
3. In **Project → Settings → Members**, add an agent that uses that backend and points to the working path on the remote machine.
4. Start a chat from the command palette. The session runs remotely, and the desktop app still shows tool approvals, questions, status, and output.

## Desktop Workflow

- **56px icon rail** for Chat, Board, Org, Hooks, and Settings.
- **Agent chat list** with pinned agents, active-session dots, and inline session counts.
- **Project sessions view** for scanning recent work by project instead of by agent.
- **Permission mode chooser** before a session starts touching files or running tools.
- **Inline subagent/tool cards** so shell, file, and MCP activity is inspectable without reading raw logs.
- **Keyboard-first navigation** for moving across agents, projects, and sessions quickly.

## Development

**Prerequisites:** [Go 1.26+](https://go.dev/), [Node.js 22+](https://nodejs.org/) with [pnpm](https://pnpm.io/), and the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation).

```bash
make install-deps    # Install frontend dependencies
make dev             # Development mode with hot reload
make test            # Backend Go tests + frontend Vitest
make build           # Production build for the current platform
make install         # Install the app bundle (macOS: /Applications/Agentre.app)
```

To target another macOS install location:

```bash
make install MACOS_APP_INSTALL_DIR="$HOME/Applications"
```

## Contributing

Issues and pull requests are welcome. Start with the [Contributing Guide](./CONTRIBUTING.md) ([中文版](./docs/CONTRIBUTING_ZH.md)) — it covers setup, the engineering ground rules, commit style, and the PR checklist.

## License

Released under [GPLv3](./LICENSE).
