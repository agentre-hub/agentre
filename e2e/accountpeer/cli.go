package accountpeer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

type result struct {
	code           int
	stdout, stderr string
}

// cli runs the freshly built agrctl against one isolated data directory.
type cli struct {
	bin, dataDir, home string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func newCLI(t *testing.T, _ *World) *cli {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "agrctl")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agrctl")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agrctl: %v\n%s", err, out)
	}
	c := &cli{bin: bin, dataDir: filepath.Join(root, "data"), home: filepath.Join(root, "home")}
	for _, d := range []string{c.dataDir, c.home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// env is a scrubbed environment: nothing inherited can point agrctl at a real desktop,
// token or data directory.
func (c *cli) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + c.home,
		"AGENTRE_DATA_DIR=" + c.dataDir,
		"AGENTRED_DATA_DIR=" + filepath.Join(c.home, "agentred"),
	}
}

// run executes agrctl. tty gives it a pseudo-terminal on stdin, which is how agrctl tells
// a person from an external program (a logged-in account may only write as a person).
func (c *cli) run(t *testing.T, tty bool, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = c.env()
	cmd.Dir = c.home
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if tty {
		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("open pty: %v", err)
		}
		defer master.Close()
		cmd.Stdin = slave
		err = cmd.Start()
		_ = slave.Close()
		if err != nil {
			t.Fatalf("start agrctl: %v", err)
		}
	} else {
		cmd.Stdin = nil // /dev/null: not a terminal
		if err := cmd.Start(); err != nil {
			t.Fatalf("start agrctl: %v", err)
		}
	}
	err := cmd.Wait()
	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("run agrctl %s: %v", strings.Join(args, " "), err)
	}
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

// credentialsFileIs0600 reports whether every regular file agrctl wrote under its data
// directory is owner-only, and at least one exists.
func (c *cli) credentialsFileIs0600(t *testing.T) bool {
	t.Helper()
	found := false
	err := filepath.Walk(c.dataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		found = true
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v", p, info.Mode().Perm())
		}
		return nil
	})
	return err == nil && found
}
