package shellloc

import (
	"path"
	"strings"
)

// WorktreeAddOf returns the absolute path a `git worktree add` in cmd (started
// in cwd) creates, or "" if there is none or it cannot be resolved. A command
// wrapped in a shell (`/bin/zsh -lc '<script>'`, `bash -c "<script>"`, the
// shape Codex reports for command_execution) is unwrapped first.
func WorktreeAddOf(cmd, cwd string) string {
	if script, ok := shellScript(cmd); ok {
		cmd = script
	}
	_, wt, _ := ParseShellLocation(cmd, cwd)
	return wt
}

var wrapperShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

// shellScript returns the script of a single `<shell> [-flags] -c <script>`
// command; ok=false for anything else.
func shellScript(cmd string) (string, bool) {
	segs, ok := tokenize(cmd)
	if !ok || len(segs) != 1 {
		return "", false
	}
	words := segs[0].words
	if len(words) < 3 || words[0].dynamic || !wrapperShells[path.Base(words[0].s)] {
		return "", false
	}
	for i := 1; i < len(words)-1; i++ {
		w := words[i]
		if w.dynamic || !strings.HasPrefix(w.s, "-") || strings.HasPrefix(w.s, "--") {
			return "", false
		}
		if strings.ContainsRune(w.s, 'c') {
			script := words[i+1]
			if script.dynamic {
				return "", false
			}
			return script.s, true
		}
	}
	return "", false
}
