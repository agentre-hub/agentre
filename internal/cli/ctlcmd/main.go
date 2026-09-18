// Package ctlcmd implements the "agrctl ctl <…>" subcommands used to control
// a running desktop without booting the Wails app.
//
// It talks to the desktop's loopback control API (ctl_svc, mounted on the
// httpgateway under /ctl/); the endpoint URL + token are read from the
// ctlendpoint handshake file the desktop writes into AppDataDir (overridable
// via --endpoint/--token or AGENTRE_CTL_ENDPOINT/AGENTRE_CTL_TOKEN).
package ctlcmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const usageText = `agrctl ctl — control a running Agentre desktop

Usage:
  agrctl ctl agents                          list configured agents
  agrctl ctl projects                        list projects
  agrctl ctl send --agent <name> [flags] <task text...>

Send flags:
  --agent <name>     target agent by name
  --agent-id <id>    target agent by id (overrides --agent)
  --project <id>     project id to run in (0 = free session)
  --wait             block until the turn finishes, then print its final text
  --isolated         one-shot isolated session (not shown in the sidebar)

Connection (all commands):
  --endpoint <url>   control endpoint URL   (env AGENTRE_CTL_ENDPOINT)
  --token <token>    control token          (env AGENTRE_CTL_TOKEN)
By default the endpoint + token are read from the desktop's handshake file.`

// Main is the process entry point; runs the subcommand and exits. Never returns.
func Main(args []string) {
	os.Exit(run(args, os.Stdout, os.Stderr, os.LookupEnv))
}

// run is the testable core; returns the exit code. lookupEnv is injected so
// tests can point the CLI at a fake control server.
func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "agents":
		return runAgents(args[1:], stdout, stderr, lookupEnv)
	case "projects":
		return runProjects(args[1:], stdout, stderr, lookupEnv)
	case "send":
		return runSend(args[1:], stdout, stderr, lookupEnv)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(stdout, usageText)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "ctl: unknown subcommand %q\n", args[0])
		return 2
	}
}

// connFlags 注册所有子命令共用的连接 flag。
func connFlags(fs *flag.FlagSet) (endpoint, token *string) {
	endpoint = fs.String("endpoint", "", "control endpoint URL (env AGENTRE_CTL_ENDPOINT)")
	token = fs.String("token", "", "control token (env AGENTRE_CTL_TOKEN)")
	return endpoint, token
}

func runAgents(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint, token := connFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ep, err := resolveEndpoint(*endpoint, *token, lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "ctl:", err)
		return 1
	}
	var out struct {
		Agents []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			SystemBadge string `json:"systemBadge"`
		} `json:"agents"`
	}
	if err := ep.Get("/ctl/v1/agents", &out); err != nil {
		_, _ = fmt.Fprintln(stderr, "ctl:", err)
		return 1
	}
	for _, a := range out.Agents {
		line := fmt.Sprintf("#%d\t%s", a.ID, a.Name)
		if a.Description != "" {
			line += "\t" + a.Description
		}
		if a.SystemBadge != "" {
			line += "\t[" + a.SystemBadge + "]"
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
	return 0
}

func runProjects(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint, token := connFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ep, err := resolveEndpoint(*endpoint, *token, lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "ctl:", err)
		return 1
	}
	var out struct {
		Projects []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"projects"`
	}
	if err := ep.Get("/ctl/v1/projects", &out); err != nil {
		_, _ = fmt.Fprintln(stderr, "ctl:", err)
		return 1
	}
	for _, p := range out.Projects {
		_, _ = fmt.Fprintf(stdout, "#%d\t%s\t%s\n", p.ID, p.Name, p.Path)
	}
	return 0
}

func runSend(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint, token := connFlags(fs)
	agent := fs.String("agent", "", "target agent name")
	agentID := fs.Int64("agent-id", 0, "target agent id (overrides --agent)")
	project := fs.Int64("project", 0, "project id (0 = free session)")
	wait := fs.Bool("wait", false, "block until the turn finishes, then print final text")
	isolated := fs.Bool("isolated", false, "one-shot isolated session (not shown in sidebar)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *agent == "" && *agentID == 0 {
		_, _ = fmt.Fprintln(stderr, "ctl send: --agent or --agent-id is required")
		return 2
	}
	if text == "" {
		_, _ = fmt.Fprintln(stderr, "ctl send: task text is required")
		return 2
	}
	ep, err := resolveEndpoint(*endpoint, *token, lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "ctl:", err)
		return 1
	}
	body := map[string]any{
		"agent":     *agent,
		"agentId":   *agentID,
		"projectId": *project,
		"text":      text,
		"wait":      *wait,
		"isolated":  *isolated,
	}
	var out struct {
		SessionID          int64  `json:"sessionId"`
		AssistantMessageID int64  `json:"assistantMessageId"`
		Text               string `json:"text"`
		Done               bool   `json:"done"`
	}
	if err := ep.Post("/ctl/v1/send", body, &out); err != nil {
		_, _ = fmt.Fprintln(stderr, "ctl:", err)
		return 1
	}
	label := *agent
	if label == "" {
		label = fmt.Sprintf("agent #%d", *agentID)
	}
	if *wait {
		_, _ = fmt.Fprintln(stdout, out.Text)
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "dispatched to %s — session #%d\n", label, out.SessionID)
	_, _ = fmt.Fprintln(stdout, out.SessionID)
	return 0
}
