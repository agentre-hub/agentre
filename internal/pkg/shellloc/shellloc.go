// Package shellloc recognizes where a shell command leaves the AI, from the
// command text alone. It is the location signal for backends that give no
// structured one (Pi, builtin, ACP): `cd <path>`, `git -C <path>` and the target
// of `git worktree add <path>`.
//
// The parser is deliberately conservative. Anything it cannot follow with
// certainty (variables, command substitution, `~`, `cd -`, subshells, pipelines,
// `||`, loops, globs, unterminated quotes) reports ok=false so the caller keeps
// its previous location instead of guessing.
package shellloc

import (
	"path"
	"strings"
)

// ParseShellLocation reports where the shell command cmd, started in cwd, leaves
// the AI.
//
//   - dir is the directory after the command: cwd advanced by every `cd <path>`
//     and by `git -C <path>` (the AI is working in that checkout). With no such
//     effect, dir == cwd and ok is true. When ok is false dir is cwd.
//   - worktreeAdd is the absolute path named by a `git worktree add` in the
//     command ("" if none). It is reported even when ok is false, as long as the
//     path itself could be resolved.
//   - ok is false when the resulting dir cannot be determined.
//
// Relative paths resolve against the running directory of the command, so
// `cd a && git -C b status` yields <cwd>/a/b. An empty cwd is "unknown": only
// absolute paths resolve.
func ParseShellLocation(cmd, cwd string) (dir string, worktreeAdd string, ok bool) {
	segs, tokOK := tokenize(cmd)
	if !tokOK {
		return cwd, "", false
	}
	cur := cwd
	ok = true
	for _, seg := range segs {
		words := skipEnvAssignments(seg.words)
		if len(words) == 0 {
			continue
		}
		head := words[0]
		if head.dynamic {
			continue
		}
		switch {
		case blockKeywords[head.s]:
			ok = false
		case head.s == "cd":
			if !seg.certain() {
				ok = false
				break
			}
			next, good := applyCd(words[1:], cur)
			if !good {
				ok = false
				break
			}
			if ok {
				cur = next
			}
		case head.s == "git":
			g := parseGit(words[1:], cur)
			if g.bad {
				ok = false
				break
			}
			if g.worktree != "" {
				worktreeAdd = g.worktree
			}
			if g.moved {
				if !seg.certain() {
					ok = false
					break
				}
				if ok {
					cur = g.dir
				}
			}
		}
	}
	if !ok {
		return cwd, worktreeAdd, false
	}
	return cur, worktreeAdd, true
}

// blockKeywords start compound commands whose body we do not follow.
var blockKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "{": true, "}": true, "function": true, "select": true,
}

type word struct {
	s       string
	dynamic bool // contains an expansion we cannot evaluate ($, `, leading ~, glob)
}

type segment struct {
	words      []word
	prev, next string // separating operators; "" at the ends of the command
}

// certain reports whether the segment runs in the main shell, unconditionally
// enough to move the AI: not part of a pipeline, background job, `||` branch or
// subshell. `&&`, `;` and newline sequence it.
func (s segment) certain() bool {
	return sequential(s.prev) && sequential(s.next)
}

func sequential(op string) bool {
	return op == "" || op == "&&" || op == ";" || op == "\n"
}

func skipEnvAssignments(words []word) []word {
	for len(words) > 0 && isAssignment(words[0].s) {
		words = words[1:]
	}
	return words
}

func isAssignment(s string) bool {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := s[j]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// tokenize splits cmd into segments of words, honoring quotes and escapes.
// ok=false for an unterminated quote.
func tokenize(cmd string) (segs []segment, ok bool) {
	var (
		words   []word
		cur     strings.Builder
		inWord  bool
		dynamic bool
		prevOp  string
	)
	flushWord := func() {
		if inWord {
			words = append(words, word{s: cur.String(), dynamic: dynamic})
		}
		cur.Reset()
		inWord, dynamic = false, false
	}
	endSegment := func(op string) {
		flushWord()
		if len(words) > 0 || op == "(" || op == ")" {
			segs = append(segs, segment{words: words, prev: prevOp, next: op})
		}
		words = nil
		prevOp = op
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\':
			if i+1 >= len(rs) {
				return nil, false
			}
			i++
			if rs[i] == '\n' {
				continue
			}
			cur.WriteRune(rs[i])
			inWord = true
		case c == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return nil, false
			}
			cur.WriteString(string(rs[i+1 : j]))
			inWord = true
			i = j
		case c == '"':
			inWord = true
			closed := false
			for i++; i < len(rs); i++ {
				d := rs[i]
				if d == '"' {
					closed = true
					break
				}
				if d == '\\' && i+1 < len(rs) {
					if n := rs[i+1]; n == '"' || n == '\\' || n == '$' || n == '`' {
						i++
						cur.WriteRune(n)
						continue
					}
				}
				if d == '$' || d == '`' {
					dynamic = true
				}
				cur.WriteRune(d)
			}
			if !closed {
				return nil, false
			}
		case c == ' ' || c == '\t' || c == '\r':
			flushWord()
		case c == '\n':
			endSegment("\n")
		case c == ';':
			endSegment(";")
		case c == '(' || c == ')':
			endSegment(string(c))
		case c == '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				i++
				endSegment("||")
			} else {
				endSegment("|")
			}
		case c == '&':
			switch {
			case i+1 < len(rs) && rs[i+1] == '&':
				i++
				endSegment("&&")
			case (i+1 < len(rs) && rs[i+1] == '>') || (cur.Len() > 0 && isRedirect(rs[i-1])):
				// 2>&1, >&2, &>file: a redirection, not a background job.
				cur.WriteRune(c)
				inWord = true
			default:
				endSegment("&")
			}
		case c == '#' && !inWord:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			i--
		default:
			if c == '$' || c == '`' || c == '*' || c == '?' || c == '[' || (c == '~' && !inWord) {
				dynamic = true
			}
			cur.WriteRune(c)
			inWord = true
		}
	}
	endSegment("")
	return segs, true
}

func isRedirect(r rune) bool { return r == '>' || r == '<' }

// resolve joins p onto base. Relative paths need a known base.
func resolve(p, base string) (string, bool) {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p), true
	}
	if base == "" {
		return "", false
	}
	return path.Join(base, p), true
}

// usable reports whether w is a plain, fully known path operand.
func usable(w word) bool { return !w.dynamic && w.s != "" }

// applyCd resolves the operand of `cd`. Option flags are skipped; no operand
// (home), `-` (previous dir) and anything dynamic are undeterminable.
func applyCd(args []word, cur string) (string, bool) {
	var operands []word
	opts := true
	for _, a := range args {
		if opts && !a.dynamic && a.s == "--" {
			opts = false
			continue
		}
		if opts && !a.dynamic && len(a.s) > 1 && a.s[0] == '-' && a.s != "-" {
			continue // -L -P -e -@
		}
		operands = append(operands, a)
	}
	if len(operands) != 1 || !usable(operands[0]) || operands[0].s == "-" {
		return "", false
	}
	return resolve(operands[0].s, cur)
}

type gitEffect struct {
	dir      string // directory selected by -C (valid when moved)
	moved    bool
	worktree string // absolute `worktree add` path
	bad      bool   // a location-relevant part could not be resolved
}

// globalOptsWithValue are git options before the subcommand that consume the
// next word when not written as --opt=value.
var globalOptsWithValue = map[string]bool{
	"-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--exec-path": true, "--super-prefix": true, "--config-env": true,
}

// worktreeAddValueOpts are `git worktree add` options consuming the next word.
var worktreeAddValueOpts = map[string]bool{"-b": true, "-B": true, "--reason": true}

func parseGit(args []word, cur string) gitEffect {
	e := gitEffect{dir: cur}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a.dynamic {
			// Only fatal when it could be a location option's operand; checked below.
			e.bad = e.bad || isGlobalLocation(args, i)
			continue
		}
		if a.s == "-C" {
			if i+1 >= len(args) || !usable(args[i+1]) {
				e.bad = true
				return e
			}
			next, ok := resolve(args[i+1].s, e.dir)
			if !ok {
				e.bad = true
				return e
			}
			e.dir, e.moved = next, true
			i++
			continue
		}
		if globalOptsWithValue[a.s] {
			i++
			continue
		}
		if strings.HasPrefix(a.s, "-") {
			continue
		}
		break
	}
	if e.bad || i+1 >= len(args) || args[i].dynamic || args[i].s != "worktree" || args[i+1].dynamic || args[i+1].s != "add" {
		return e
	}
	rest := args[i+2:]
	for j := 0; j < len(rest); j++ {
		a := rest[j]
		if !a.dynamic && a.s == "--" {
			if j+1 < len(rest) {
				j++
				return withWorktree(e, rest[j])
			}
			return e
		}
		if !a.dynamic && worktreeAddValueOpts[a.s] {
			j++
			continue
		}
		if !a.dynamic && strings.HasPrefix(a.s, "-") && a.s != "-" {
			continue
		}
		return withWorktree(e, a)
	}
	return e
}

// isGlobalLocation reports whether a dynamic word sits where -C would take its
// operand (right after -C). A dynamic word elsewhere is harmless.
func isGlobalLocation(args []word, i int) bool {
	return i > 0 && !args[i-1].dynamic && args[i-1].s == "-C"
}

func withWorktree(e gitEffect, w word) gitEffect {
	if !usable(w) {
		e.bad = true
		return e
	}
	p, ok := resolve(w.s, e.dir)
	if !ok {
		e.bad = true
		return e
	}
	e.worktree = p
	return e
}
