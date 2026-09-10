package codex

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/cliprocess"
)

var (
	ErrBinaryNotFound = errors.New("codex: codex binary not found in PATH or configured CLIPath")
	ErrProcessDead    = errors.New("codex: process exited unexpectedly")
	ErrProtocol       = errors.New("codex: app-server protocol violation")
)

type ExitError struct {
	Err    error
	Stderr string
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	stderr := strings.TrimSpace(sanitizeDiagnostic(e.Stderr))
	if stderr == "" {
		return fmt.Sprintf("codex: process exited: %v", e.Err)
	}
	return fmt.Sprintf("codex: process exited: %v: %s", e.Err, stderr)
}

func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type procOptions = cliprocess.Options
type processHandle = cliprocess.Handle

type appServerRunner interface {
	Start(ctx context.Context, opts procOptions) (processHandle, error)
}

type execAppServerRunner struct{}

func (r execAppServerRunner) Start(ctx context.Context, opts procOptions) (processHandle, error) {
	return cliprocess.Start(ctx, opts, ErrBinaryNotFound)
}

type lockedBuffer = cliprocess.LockedBuffer

var (
	bearerCredentialRE = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;"']+`)
	openAIKeyRE        = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	credentialValueRE  = regexp.MustCompile(`(?i)((?:[a-z0-9_]*(?:api[_-]?key|token|secret|authorization))["']?\s*[:=]\s*["']?)[^\s,;"']+`)
)

func sanitizeDiagnostic(value string) string {
	value = bearerCredentialRE.ReplaceAllString(value, `${1}[REDACTED]`)
	value = openAIKeyRE.ReplaceAllString(value, `[REDACTED]`)
	return credentialValueRE.ReplaceAllString(value, `${1}[REDACTED]`)
}
