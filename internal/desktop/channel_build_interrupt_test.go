//go:build !windows

package desktop

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMakeChannelRewriteIsRestoredWhenInterrupted runs the real `make build` / `make dev`
// recipe that temporarily rewrites a tracked file to the channel identity, under dash —
// the /bin/sh GNU make uses on Debian/Ubuntu — and interrupts it with Ctrl-C (SIGINT to
// the whole process group) while wails is running. dash does not run an EXIT trap when it
// is killed by a signal, so the tracked file must still be restored.
func TestMakeChannelRewriteIsRestoredWhenInterrupted(t *testing.T) {
	dash, err := exec.LookPath("dash")
	if err != nil {
		t.Skip("dash is not installed")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	root := repoRoot(t)

	for _, tc := range []struct {
		goal string
		file string
	}{
		{"build", "wails.json"},
		{"dev", filepath.Join("build", "darwin", "Info.dev.plist")},
	} {
		t.Run("Given make "+tc.goal+" CHANNEL=beta rewrote "+tc.file+" When Ctrl-C hits wails Then "+tc.file+" is restored", func(t *testing.T) {
			work := t.TempDir()
			original, err := os.ReadFile(filepath.Join(root, tc.file)) //nolint:gosec // G304: repo file
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(work, tc.file)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, original, 0o600); err != nil { //nolint:gosec // G703: temp path
				t.Fatal(err)
			}

			// The fake wails snapshots the file it was started with (proving the rewrite
			// happened), then plays the user's Ctrl-C on its own process group.
			during := filepath.Join(work, "during")
			fakeWails := filepath.Join(work, "fake-wails")
			script := "#!/bin/sh\ncp '" + tc.file + "' '" + during + "'\nkill -INT 0\nsleep 5\n"
			if err := os.WriteFile(fakeWails, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("make", "-n", "-C", root, tc.goal, "CHANNEL=beta", "WAILS="+fakeWails) //nolint:gosec // G204: test-controlled args
			cmd.Env = withoutEnv(os.Environ(), "CHANNEL")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make -n %s: %v\n%s", tc.goal, err, out)
			}
			recipe := string(out)
			start := strings.Index(recipe, "backup=")
			wailsAt := strings.Index(recipe, fakeWails)
			if start < 0 || wailsAt < start {
				t.Fatalf("make %s recipe has no backup/rewrite/wails sequence:\n%s", tc.goal, recipe)
			}
			end := wailsAt + strings.IndexByte(recipe[wailsAt:], '\n')
			if end < wailsAt {
				end = len(recipe)
			}
			line := strings.ReplaceAll(recipe[start:end], "scripts/channel-identity.sh", filepath.Join(root, "scripts", "channel-identity.sh"))

			run := exec.Command(dash, "-c", line) //nolint:gosec // G204: recipe printed by make -n
			run.Dir = work
			run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			done := make(chan []byte, 1)
			go func() {
				o, _ := run.CombinedOutput()
				done <- o
			}()
			var runOut []byte
			select {
			case runOut = <-done:
			case <-time.After(3 * time.Minute):
				t.Fatal("interrupted recipe did not exit")
			}

			rewritten, err := os.ReadFile(during) //nolint:gosec // G304: temp path
			if err != nil {
				t.Fatalf("fake wails never ran: %v\n%s", err, runOut)
			}
			if !bytes.Contains(rewritten, []byte("Agentre Beta")) {
				t.Fatalf("%s was not rewritten to the beta identity before wails ran:\n%s", tc.file, rewritten)
			}
			got, err := os.ReadFile(target) //nolint:gosec // G304: temp path
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, original) {
				t.Fatalf("%s left rewritten after Ctrl-C:\n%s\nrecipe output:\n%s", tc.file, got, runOut)
			}
		})
	}
}
