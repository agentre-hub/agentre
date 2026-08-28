package cliprober

import (
	"context"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/pkg/codex"
)

func probeCodex(ctx context.Context, req ProbeRequest) (*ProbeResponse, error) {
	binary := strings.TrimSpace(req.CLIPath)
	if binary == "" {
		binary = "codex"
	}
	cwd, err := os.MkdirTemp("", "agentre-codex-test-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(cwd) }()

	opts := []codex.Option{
		codex.WithBinary(binary),
		codex.WithCwd(cwd),
		codex.WithEnv(req.Env),
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		opts = append(opts, codex.WithModel(model))
	}
	// CodexConfigs 由上层（主进程 service / daemon handler）通过
	// agentruntime.BuildCodexConfig 装配后塞进来；cliprober 自身不依赖 agentruntime。
	for _, c := range req.CodexConfigs {
		opts = append(opts, codex.WithConfig(c))
	}
	if sb := strings.TrimSpace(req.Sandbox); sb != "" {
		opts = append(opts, codex.WithSandbox(codex.SandboxMode(sb)))
	}
	if ap := strings.TrimSpace(req.Approval); ap != "" {
		opts = append(opts, codex.WithApproval(codex.ApprovalPolicy(ap)))
	}

	r := codex.New(opts...)
	defer func() { _ = r.Close(ctx) }()
	text, err := r.Text(ctx, fixedTestPrompt)
	if err != nil {
		return nil, wrapCLIProberError(err)
	}
	return &ProbeResponse{Text: text}, nil
}
