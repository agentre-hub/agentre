package acp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ProbeRequest 是一次 ACP「测试连接」的入参:起 acpCommand + acpArgs 的子进程
// 并完成 initialize 握手。
type ProbeRequest struct {
	Command string
	Args    []string
}

// Probe 起 ACP agent 子进程、跑 initialize 握手、校验 protocolVersion==1,
// 然后关掉进程返回。照 hermesProber 的口径「握手即止」:不跑 session/new、
// 不跑一轮 prompt —— agent turn 属于 chat 路径。
//
// 返回文案带上 agentInfo.name/title/version 与协商到的关键能力
// (image / mcp-http / loadSession);authMethods 非空时把方法名并列展示
// (hermes 已登录时也会列出 authMethods,非空不等于失败)。
func Probe(ctx context.Context, req ProbeRequest) (string, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return "", errors.New("acp probe: acpCommand is required")
	}
	cwd, err := os.MkdirTemp("", "agentre-acp-probe-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(cwd) }()

	s, err := spawnAgent(ctx, launchSpec{command: command, args: req.Args, cwd: cwd})
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close(context.Background()) }()

	caps := s.caps
	name := caps.agentName
	if name == "" {
		name = "acp agent"
	}
	if caps.agentTitle != "" {
		name = caps.agentTitle
	}
	version := caps.agentVersion
	var b strings.Builder
	if version != "" {
		fmt.Fprintf(&b, "%s %s", name, version)
	} else {
		b.WriteString(name)
	}
	fmt.Fprintf(&b, " (ACP v1; image=%t, mcp-http=%t, loadSession=%t)",
		caps.promptImage, caps.mcpHTTP, caps.loadSession)
	if len(caps.authMethods) > 0 {
		fmt.Fprintf(&b, "; auth: %s", strings.Join(caps.authMethods, ", "))
	}
	return b.String(), nil
}
