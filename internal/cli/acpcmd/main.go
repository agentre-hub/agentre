// Package acpcmd implements "agrctl acp": agrctl running as an ACP v1 Agent on
// stdio, so an Agentre `acp` backend can drive it with
// `--acp-command agrctl --acp-args acp --agent <name>`.
//
// It dispatches each prompt through the desktop's loopback control API
// (/ctl/v1/*) to the configured Agentre agent and streams the turn's events
// back as ACP session/update notifications. Like the rest of agrctl it imports
// only internal/cli/* + lightweight internal/pkg/* packages (no services, no
// DB), so the companion binary stays small and fast to spawn.
package acpcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

const usageText = `agrctl acp — run agrctl as an ACP v1 agent over stdio

Usage:
  agrctl acp --agent <name> [--project <id>] [connection flags]

The agent creates one Agentre session per ACP session/new and dispatches each
prompt through the running desktop's ctl endpoint (/ctl/v1/*), streaming the
turn's chat events back as session/update notifications. Images in prompts are
forwarded to the target agent.

Target (one of --agent / --agent-id is required):
  --agent <name>     target Agentre agent by name
  --agent-id <id>    target Agentre agent by id
  --project <id>     project id to run in (0 = free session)

Connection:
  --endpoint <url>   control endpoint URL   (env AGENTRE_CTL_ENDPOINT)
  --token <token>    control token          (env AGENTRE_CTL_TOKEN)
By default the endpoint + token are read from the desktop's handshake file.`

// Main is the process entry point; runs the ACP agent and exits. Never returns.
func Main(args []string) {
	os.Exit(run(args, os.Stdin, os.Stdout, os.Stderr, os.LookupEnv))
}

// options 是 acp 子命令解析后的配置。
type options struct {
	Endpoint ctlclient.Endpoint
	Agent    string
	AgentID  int64
	Project  int64
}

// run is the testable core; returns the exit code. It blocks serving the ACP
// connection until the client disconnects (or the connection fails).
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	opts, err := parseOptions(args, stderr, lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "acp:", err)
		return 2
	}
	ag := newAgent(opts)
	conn := acpsdk.NewAgentSideConnection(ag, stdout, stdin)
	ag.SetAgentConnection(conn)
	<-conn.Done()
	return 0
}

// parseOptions 解析 flag 并解析控制端点(flag > env > AppDataDir 握手文件)。
func parseOptions(args []string, stderr io.Writer, lookupEnv func(string) (string, bool)) (options, error) {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprintln(stderr, usageText) }
	agent := fs.String("agent", "", "target Agentre agent name")
	agentID := fs.Int64("agent-id", 0, "target Agentre agent id (overrides --agent)")
	project := fs.Int64("project", 0, "project id (0 = free session)")
	endpoint := fs.String("endpoint", "", "control endpoint URL (env AGENTRE_CTL_ENDPOINT)")
	token := fs.String("token", "", "control token (env AGENTRE_CTL_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if strings.TrimSpace(*agent) == "" && *agentID <= 0 {
		return options{}, errors.New("--agent or --agent-id is required")
	}
	ep, err := ctlclient.Resolve(*endpoint, *token, lookupEnv)
	if err != nil {
		return options{}, err
	}
	return options{
		Endpoint: ep,
		Agent:    strings.TrimSpace(*agent),
		AgentID:  *agentID,
		Project:  *project,
	}, nil
}
