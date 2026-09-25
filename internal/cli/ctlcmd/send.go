package ctlcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

// runSend 派发一条任务给某个 agent（`agrctl send`）。
func runSend(args []string, s *sys) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	endpoint := fs.String("endpoint", "", "control endpoint URL (env AGENTRE_CTL_ENDPOINT)")
	token := fs.String("token", "", "control token (env AGENTRE_CTL_TOKEN)")
	agent := fs.String("agent", "", "target agent name")
	agentID := fs.Int64("agent-id", 0, "target agent id (overrides --agent)")
	project := fs.Int64("project", 0, "project id (0 = free session)")
	wait := fs.Bool("wait", false, "block until the turn finishes, then print final text")
	isolated := fs.Bool("isolated", false, "one-shot isolated session (not shown in sidebar)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(s.stdout, sendUsage)
			return nil
		}
		return usageErrorf("send: %v", err)
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *agent == "" && *agentID == 0 {
		return usageErrorf("send: --agent or --agent-id is required")
	}
	if text == "" {
		return usageErrorf("send: task text is required")
	}
	ep, err := ctlclient.Resolve(*endpoint, *token, s.lookupEnv)
	if err != nil {
		return err
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
		return err
	}
	if *wait {
		_, _ = fmt.Fprintln(s.stdout, out.Text)
		return nil
	}
	label := *agent
	if label == "" {
		label = fmt.Sprintf("agent #%d", *agentID)
	}
	_, _ = fmt.Fprintf(s.stderr, "dispatched to %s — session #%d\n", label, out.SessionID)
	_, _ = fmt.Fprintln(s.stdout, out.SessionID)
	return nil
}
