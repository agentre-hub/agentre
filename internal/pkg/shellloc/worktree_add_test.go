package shellloc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// `git worktree add <路径>` 是对任何后端都成立的认领候选:Claude Code 的 Bash 入参是裸命令,
// Codex 的 command_execution 报的是包在登录 shell 里的整行(`/bin/zsh -lc '…'`)。
func TestWorktreeAddOf(t *testing.T) {
	rows := []struct{ name, cmd, cwd, want string }{
		{"bare relative", "git worktree add ../wt -b feat", "/w/repo", "/w/wt"},
		{"after cd", "cd /r && git worktree add wt", "/w", "/r/wt"},
		{"zsh -lc single quoted", `/bin/zsh -lc 'git worktree add ../wt -b feat'`, "/w/repo", "/w/wt"},
		{"bash -c double quoted", `bash -c "cd /r && git worktree add .worktrees/x"`, "/w", "/r/.worktrees/x"},
		{"sh -c absolute", `/bin/sh -c 'git worktree add /tmp/x'`, "", "/tmp/x"},
		{"no worktree add", `/bin/zsh -lc 'git status'`, "/w", ""},
		{"not a shell wrapper", `python -c 'git worktree add ../x'`, "/w", ""},
		{"relative with unknown cwd", "git worktree add ../wt", "", ""},
		{"unterminated", `/bin/zsh -lc 'git worktree add ../wt`, "/w", ""},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			assert.Equal(t, r.want, WorktreeAddOf(r.cmd, r.cwd))
		})
	}
}
