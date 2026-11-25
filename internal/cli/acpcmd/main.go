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

	"github.com/agentre-hub/agentre/internal/cli/ctlcmd"
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
By default the endpoint + token are read from the desktop's handshake file.

Account mode: when this machine is logged in (agrctl login) or AGENTRE_TOKEN is set,
the prompts go to your account instead, straight to the machine that runs the agent.
That needs a personal token with the chat and approve scopes. Create it at your own
terminal and put it in the editor's agent server configuration:
  agrctl create token --name editor --scope chat,approve
  {"command": "agrctl", "args": ["acp", "--agent", "<name>"], "env": {"AGENTRE_TOKEN": "<the token>"}}
(AGENTRE_SERVER_URL selects another server.) Without the token, agrctl acp fails
with this hint. --endpoint keeps using the desktop.`

// Main is the process entry point; runs the ACP agent and exits. Never returns.
func Main(args []string) {
	os.Exit(run(args, os.Stdin, os.Stdout, os.Stderr, os.LookupEnv))
}

// options 是 acp 子命令解析后的配置。
type options struct {
	Endpoint ctlclient.Endpoint
	// Account 非 nil 表示账号模式（个人 token）；Endpoint 此时为空。
	Account accountDriver
	Agent   string
	AgentID int64
	Project int64
}

// run is the testable core; returns the exit code. It blocks serving the ACP
// connection until the client disconnects (or the connection fails).
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	opts, err := parseOptions(args, stderr, lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "acp:", err)
		return 2
	}
	if opts.Account != nil {
		defer opts.Account.Close()
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
	ep, drv, err := ctlcmd.ResolveACPExecutor(*endpoint, *token, lookupEnv, stderr)
	if err != nil {
		return options{}, err
	}
	opts := options{
		Endpoint: ep,
		Agent:    strings.TrimSpace(*agent),
		AgentID:  *agentID,
		Project:  *project,
	}
	if drv != nil { // 不能直接写进字面量：nil *AccountDriver 装进接口不是 nil
		opts.Account = drv
	}
	return opts, nil
}
