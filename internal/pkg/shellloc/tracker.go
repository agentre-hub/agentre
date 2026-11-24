package shellloc

import (
	"bytes"
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"sync"
)

// WorktreeAddKey is the key a Tracker adds to a shell tool call's JSON input
// when the command contains `git worktree add <path>`; the value is the
// absolute path. Consumers read it as a claim candidate.
const WorktreeAddKey = "agentre_worktree_add"

// Tracker follows the AI's current directory across the shell calls of one
// session, for backends with no structured location signal. Safe for
// concurrent use.
type Tracker struct {
	mu    sync.Mutex
	dir   string
	calls map[string]callRec // call id -> what it recorded, until its result
}

type callRec struct{ dir, worktreeAdd string }

// NewTracker starts at start (the session working directory; "" = unknown).
func NewTracker(start string) *Tracker {
	return &Tracker{dir: start, calls: map[string]callRec{}}
}

// Dir is the current location.
func (t *Tracker) Dir() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dir
}

// Call records the shell tool call id whose JSON input carries a "command" and
// returns the location after the command (what ToolCall.Cwd should hold) plus
// the input, with WorktreeAddKey added when the command creates a worktree. A
// command it cannot read or follow leaves the location, and the input,
// unchanged. A call id seen again (an incremental re-emit) is not applied twice.
func (t *Tracker) Call(id string, input []byte) (cwd string, out []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, seen := t.calls[id]
	if !seen {
		rec = callRec{dir: t.dir}
		var args struct {
			Command *string `json:"command"`
		}
		if json.Unmarshal(input, &args) == nil && args.Command != nil {
			dir, wt, ok := ParseShellLocation(*args.Command, t.dir)
			if ok {
				t.dir = dir
			}
			rec = callRec{dir: t.dir, worktreeAdd: wt}
		}
		t.calls[id] = rec
	}
	out = input
	if rec.worktreeAdd != "" {
		out = withWorktreeAdd(input, rec.worktreeAdd)
	}
	return rec.dir, out
}

// Here records a non-shell tool call id at the current location and returns it.
func (t *Tracker) Here(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rec, seen := t.calls[id]; seen {
		return rec.dir
	}
	t.calls[id] = callRec{dir: t.dir}
	return t.dir
}

// Result returns the location recorded for call id (what ToolResult.Cwd should
// hold) and forgets it; an unknown id gets the current location.
func (t *Tracker) Result(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if rec, ok := t.calls[id]; ok {
		delete(t.calls, id)
		return rec.dir
	}
	return t.dir
}

var windowsAbs = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// AbsDir returns p cleaned if it is an absolute directory (POSIX or Windows
// drive form), else "". Structured location signals from backends are only
// trusted when absolute.
func AbsDir(p string) string {
	switch {
	case p == "":
		return ""
	case path.IsAbs(p):
		return path.Clean(p)
	case windowsAbs.MatchString(p), filepath.IsAbs(p):
		return p
	}
	return ""
}

// withWorktreeAdd splices WorktreeAddKey into a JSON object, leaving the other
// bytes untouched. Non-objects are returned as is.
func withWorktreeAdd(in []byte, p string) []byte {
	trimmed := bytes.TrimRight(in, " \t\r\n")
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' || !json.Valid(in) {
		return in
	}
	val, err := json.Marshal(p)
	if err != nil {
		return in
	}
	body := bytes.TrimRight(trimmed[:len(trimmed)-1], " \t\r\n")
	sep := []byte(",")
	if len(body) == 0 || body[len(body)-1] == '{' {
		sep = nil
	}
	var b bytes.Buffer
	b.Write(body)
	b.Write(sep)
	b.WriteString(`"` + WorktreeAddKey + `":`)
	b.Write(val)
	b.WriteByte('}')
	return b.Bytes()
}

// Registry keeps one Tracker per session key so a cd survives across turns.
// The zero value is ready to use.
type Registry struct {
	mu sync.Mutex
	m  map[int64]*Tracker
}

func NewRegistry() *Registry { return &Registry{m: map[int64]*Tracker{}} }

// For returns the session's tracker, creating it at start on first use.
func (r *Registry) For(key int64, start string) *Tracker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[int64]*Tracker{}
	}
	t, ok := r.m[key]
	if !ok {
		t = NewTracker(start)
		r.m[key] = t
	}
	return t
}
