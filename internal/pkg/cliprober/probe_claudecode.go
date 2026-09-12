package cliprober

import (
	"context"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// fixedTestPrompt 与 service/agent_backend_svc/agent_backend.go 中保持字面一致；
// 两处独立各持一份，避免 cliprober 反向依赖 service 层。
const fixedTestPrompt = "Reply with the single word 'pong' and nothing else."

func probeClaudeCode(ctx context.Context, req ProbeRequest) (*ProbeResponse, error) {
	binary := strings.TrimSpace(req.CLIPath)
	if binary == "" {
		binary = "claude"
	}
	cwd, err := os.MkdirTemp("", "agentre-claudecode-test-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(cwd) }()

	opts := []claudecode.Option{
		claudecode.WithBinary(binary),
		claudecode.WithCwd(cwd),
		claudecode.WithEnv(req.Env),
		claudecode.WithSettingsEnv(req.Env),
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		opts = append(opts, claudecode.WithModel(model))
	}
	r := claudecode.New(opts...)
	defer func() { _ = r.Close(ctx) }()
	text, err := r.Text(ctx, fixedTestPrompt, claudecode.MaxTurns(1))
	if err != nil {
		return nil, wrapCLIProberError(err)
	}
	return &ProbeResponse{Text: text}, nil
}
