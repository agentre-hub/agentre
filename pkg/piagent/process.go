package piagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/cliprocess"
)

type ExitError struct {
	// Err is retained for compatibility, but errors produced by pkg/piagent store
	// only a normalized exit classification here. Raw command/process errors must
	// not cross the package boundary.
	Err error
	// Stderr is retained for compatibility with callers that construct ExitError
	// values themselves. pkg/piagent-produced errors always leave it empty.
	Stderr string
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	cause := normalizeProcessExitCause(e.Err)
	if cause == nil {
		return "piagent: process exited"
	}
	return fmt.Sprintf("piagent: process exited: %s", cause.Error())
}

func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return normalizeProcessExitCause(e.Err)
}

func (e *ExitError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	cause := normalizeProcessExitCause(e.Err)
	if errors.Is(cause, target) {
		return true
	}
	targetCause := normalizeProcessExitCause(target)
	if classified, ok := targetCause.(*processExitCause); ok && classified.classification == "process failure" {
		return false
	}
	return cause != nil && targetCause != nil && cause.Error() == targetCause.Error()
}

type processExitCause struct {
	classification string
}

func (e *processExitCause) Error() string {
	if e == nil {
		return ""
	}
	return e.classification
}

func normalizeProcessExitCause(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, ErrBinaryNotFound) {
		return ErrBinaryNotFound
	}
	if errors.Is(err, ErrProcessDead) {
		return ErrProcessDead
	}
	if errors.Is(err, ErrSessionNotFound) {
		return ErrSessionNotFound
	}
	if classified, ok := err.(*processExitCause); ok {
		return classified
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &processExitCause{classification: fmt.Sprintf("exit status %d", exitErr.ExitCode())}
	}
	text := strings.TrimSpace(err.Error())
	if isSafeExitStatus(text) || isSafeSignalStatus(text) {
		return &processExitCause{classification: text}
	}
	return &processExitCause{classification: "process failure"}
}

func isSafeExitStatus(text string) bool {
	const prefix = "exit status "
	if !strings.HasPrefix(text, prefix) {
		return false
	}
	code := strings.TrimPrefix(text, prefix)
	if code == "" || strings.TrimSpace(code) != code {
		return false
	}
	_, err := strconv.Atoi(code)
	return err == nil
}

func isSafeSignalStatus(text string) bool {
	const prefix = "signal: "
	if !strings.HasPrefix(text, prefix) {
		return false
	}
	signal := strings.TrimPrefix(text, prefix)
	if signal == "" || len(signal) > 32 {
		return false
	}
	for _, r := range signal {
		if (r < 'a' || r > 'z') && r != ' ' && r != '-' {
			return false
		}
	}
	return true
}

func processBoundaryError(operation, command string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, ErrBinaryNotFound) {
		return ErrBinaryNotFound
	}
	if errors.Is(err, ErrProcessDead) {
		return ErrProcessDead
	}
	if errors.Is(err, ErrSessionNotFound) {
		return ErrSessionNotFound
	}
	if operation == "read" && isFrameSafetyLimitError(err) {
		return errors.New("piagent: process read failed: token too long")
	}
	command = safeRPCCommand(command)
	if command == "request" {
		return fmt.Errorf("piagent: process %s failed", operation)
	}
	return fmt.Errorf("piagent: %s %s failed", command, operation)
}

func isFrameSafetyLimitError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "token too long")
}

func safeRPCCommand(command string) string {
	switch strings.TrimSpace(command) {
	case "get_state", "fork", "get_entries", "prompt", "compact", "get_session_stats", "get_commands", "steer", "abort":
		return strings.TrimSpace(command)
	default:
		return "request"
	}
}

func requestCommand(v any) string {
	request, ok := v.(map[string]any)
	if !ok {
		return "request"
	}
	command, _ := request["type"].(string)
	return safeRPCCommand(command)
}

func missingSessionIdentity(stderr string) (string, bool) {
	const marker = "no session found matching"
	trimmed := strings.TrimSpace(stderr)
	lower := strings.ToLower(trimmed)
	index := strings.Index(lower, marker)
	if index < 0 {
		return "", false
	}
	remainder := strings.TrimSpace(trimmed[index+len(marker):])
	if remainder == "" {
		return "", true
	}
	var candidate string
	if remainder[0] == '\'' || remainder[0] == '"' {
		quote := remainder[0]
		if end := strings.IndexByte(remainder[1:], quote); end >= 0 {
			candidate = remainder[1 : end+1]
		}
	} else {
		candidate = strings.Fields(remainder)[0]
	}
	candidate = strings.TrimSpace(candidate)
	if !isSafeSessionIdentity(candidate) {
		return "", true
	}
	return candidate, true
}

func isSafeSessionIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("-_.:/\\", r):
		default:
			return false
		}
	}
	return true
}

type procOptions = cliprocess.Options
type processHandle = cliprocess.Handle

type processRunner interface {
	Start(ctx context.Context, opts procOptions) (processHandle, error)
}

// execProcessRunner 走公共底座:进程组、树杀/树信号、spawn 前的取消检查与 binary
// 解析都归 internal/pkg/cliprocess,这里只负责把 piagent 的 sentinel 交给它。
type execProcessRunner struct{}

func (r execProcessRunner) Start(ctx context.Context, opts procOptions) (processHandle, error) {
	// 进程寿命不绑在轮 ctx 上:stream 自带一段取消后的 settlement 窗口,之后才由它
	// 终结这棵进程树(见 rpcProcess.terminate)。cliprocess.Start 只用 ctx 守 spawn 阶段。
	return cliprocess.Start(ctx, opts, ErrBinaryNotFound)
}

type lockedBuffer = cliprocess.LockedBuffer
