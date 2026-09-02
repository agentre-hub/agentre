package piagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildRPCArgsIncludesSystemPromptAndThinking(t *testing.T) {
	args := buildRPCArgs(&Client{
		sessionDir:   "/data/pi-sessions",
		model:        "gpt-5.5",
		thinking:     "high",
		systemPrompt: "be concise",
	})

	assert.Equal(t, []string{
		"--mode", "rpc",
		"--session-dir", "/data/pi-sessions",
		"--append-system-prompt", "be concise",
		"--model", "gpt-5.5",
		"--thinking", "high",
	}, args)
}

// session 非空时下发 --session，让 Pi 跨 turn resume 同一会话；--session-dir 用
// 专用目录而非 cwd，避免 session 文件污染工作目录。
func TestBuildRPCArgsResumesSession(t *testing.T) {
	args := buildRPCArgs(&Client{
		cwd:        "/work/project", // 工作目录不应进 args（它只作 cmd.Dir）
		sessionDir: "/data/pi-sessions",
		session:    "/data/pi-sessions/agentre-7.jsonl",
	})

	assert.Equal(t, []string{
		"--mode", "rpc",
		"--session-dir", "/data/pi-sessions",
		"--session", "/data/pi-sessions/agentre-7.jsonl",
	}, args)
}

// TestBuildRPCArgsPassesThroughMaxThinking 锁住 spec 2026-09-01「三后端下发档位的
// 收敛」:max 原样下发 --thinking max,不再被本地兼容折叠成 xhigh —— pi --help 明写
// --thinking 支持 max。
func TestBuildRPCArgsPassesThroughMaxThinking(t *testing.T) {
	args := buildRPCArgs(&Client{thinking: "max"})
	assert.Equal(t, []string{"--mode", "rpc", "--thinking", "max"}, args)
}

func TestBuildRPCArgsOmitsInvalidThinking(t *testing.T) {
	args := buildRPCArgs(&Client{thinking: "ultra"})
	assert.Equal(t, []string{"--mode", "rpc"}, args)
}

func TestBuildRPCArgs_Extensions(t *testing.T) {
	c := New(WithExtension("/a/x.mjs"), WithExtension("/a/y.mjs"))
	args := buildRPCArgs(c)
	assert.Equal(t, []string{
		"--mode", "rpc",
		"--extension", "/a/x.mjs",
		"--extension", "/a/y.mjs",
	}, args)
}

func TestBuildRPCArgsUsesEphemeralNativeSession(t *testing.T) {
	args := buildRPCArgs(New(WithNoSession()))

	assert.Equal(t, []string{"--mode", "rpc", "--no-session"}, args)
	assert.NotContains(t, args, "--session-dir")
}

func TestBuildRPCArgs_NoExtensionByDefault(t *testing.T) {
	c := New()
	for _, a := range buildRPCArgs(c) {
		if a == "--extension" {
			t.Fatalf("did not expect --extension by default: %v", buildRPCArgs(c))
		}
	}
}
