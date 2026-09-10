package cliprober

import (
	"context"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/pkg/piagent"
)

func probePiAgent(ctx context.Context, req ProbeRequest) (*ProbeResponse, error) {
	binary := strings.TrimSpace(req.CLIPath)
	if binary == "" {
		binary = "pi"
	}
	cwd, err := os.MkdirTemp("", "agentre-piagent-test-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(cwd) }()

	opts := []piagent.Option{
		piagent.WithBinary(binary),
		piagent.WithCwd(cwd),
		// 连通性探测不应出现在用户的 Pi Session 列表中。
		piagent.WithNoSession(),
		piagent.WithEnv(req.Env),
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		opts = append(opts, piagent.WithModel(model))
	}
	// 绑定供应商时上层把物化好的 provider 扩展塞进 Extensions，与 chat run
	// 同一 --extension 注入通道（mcpbridge / provider 扩展并列）。
	for _, ext := range req.Extensions {
		opts = append(opts, piagent.WithExtension(ext))
	}
	r := piagent.New(opts...)
	defer func() { _ = r.Close(ctx) }()
	text, err := r.Text(ctx, fixedTestPrompt)
	if err != nil {
		return nil, wrapCLIProberError(err)
	}
	return &ProbeResponse{Text: text}, nil
}
