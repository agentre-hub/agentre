// Guard test for task 7 of the build-channels plan: CI must gate release.yml on the
// exact two accepted tag shapes, every wails build (all three workflows) must carry
// the paths.buildChannel ldflag, each workflow must resolve the right channel, and
// packaging must use the channel identity script's output instead of the old
// hard-coded Agentre.app / usr/bin/agentre / Name=Agentre literals. This is a source
// review, not an execution of the real workflow: GitHub Actions itself only runs on a
// real push/schedule/dispatch (see docs/specs/2026-09-17-build-channels.md "Testing
// decisions"), so the checks below read the committed YAML text and, for the tag
// gate, actually execute its extracted shell body against sample tags.
package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const channelLdflagFragment = "github.com/agentre-hub/agentre/internal/pkg/paths.buildChannel="

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "workflows", name)
	data, err := os.ReadFile(path) //nolint:gosec // G304: fixed repo-relative workflow path
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// extractRunBlock pulls the dedented body of the first `run: |` block scalar that
// follows marker in content, i.e. the shell script GitHub Actions would execute for
// that step. It does not evaluate any `${{ }}` expression — the step under test here
// deliberately reads its inputs from a plain env var so it stays directly executable.
func extractRunBlock(t *testing.T, content, marker string) string {
	t.Helper()
	idx := strings.Index(content, marker)
	if idx < 0 {
		t.Fatalf("marker %q not found in workflow", marker)
	}
	rest := content[idx:]
	const runKey = "run: |"
	runIdx := strings.Index(rest, runKey)
	if runIdx < 0 {
		t.Fatalf("no %q block scalar after marker %q", runKey, marker)
	}
	lineStart := strings.LastIndex(rest[:runIdx], "\n") + 1
	indent := runIdx - lineStart

	var raw []string
	for _, line := range strings.Split(rest[runIdx+len(runKey):], "\n") {
		if strings.TrimSpace(line) == "" {
			raw = append(raw, "")
			continue
		}
		leading := len(line) - len(strings.TrimLeft(line, " "))
		if leading <= indent {
			break
		}
		raw = append(raw, line)
	}
	if len(raw) == 0 {
		t.Fatalf("empty run block after marker %q", marker)
	}

	minIndent := -1
	for _, l := range raw {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := len(l) - len(strings.TrimLeft(l, " "))
		if minIndent == -1 || lead < minIndent {
			minIndent = lead
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	for i, l := range raw {
		if len(l) >= minIndent {
			raw[i] = l[minIndent:]
		}
	}
	return strings.Join(raw, "\n")
}

// TestWorkflowReleaseTagGate proves release.yml's tag validation accepts exactly
// vX.Y.Z and vX.Y.Z-beta.N (design decisions 5, 6, 9) and rejects every other shape,
// including -rc, before any build job — by running the extracted gate script itself
// against the sample tags called out in the task (v1.2.3, v1.2.3-beta.4, v1.2.3-rc.1,
// v1.2, 1.2.3).
func TestWorkflowReleaseTagGate(t *testing.T) {
	script := extractRunBlock(t, readWorkflow(t, "release.yml"), "Validate tag shape and resolve channel")

	cases := []struct {
		tag         string
		acceptedAs  string // "" when the tag must be rejected
		wantChannel string
		wantPre     string
	}{
		{tag: "v1.2.3", acceptedAs: "stable", wantChannel: "stable", wantPre: "false"},
		{tag: "v1.2.3-beta.4", acceptedAs: "beta", wantChannel: "beta", wantPre: "true"},
		{tag: "v1.2.3-rc.1"},
		{tag: "v1.2"},
		{tag: "1.2.3"},
	}
	for _, tc := range cases {
		name := "Given tag " + tc.tag + " When the gate runs Then it "
		if tc.acceptedAs != "" {
			name += "accepts it as " + tc.acceptedAs
		} else {
			name += "rejects it before any build job"
		}
		t.Run(name, func(t *testing.T) {
			outPath := filepath.Join(t.TempDir(), "github_output")
			if err := os.WriteFile(outPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script) //nolint:gosec // G204: fixed extracted script, test-controlled env
			cmd.Env = append(os.Environ(), "TAG="+tc.tag, "GITHUB_OUTPUT="+outPath)
			out, runErr := cmd.CombinedOutput()

			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					t.Fatalf("run extracted gate script: %v\n%s", runErr, out)
				}
				exitCode = exitErr.ExitCode()
			}

			if tc.acceptedAs == "" {
				if exitCode == 0 {
					t.Fatalf("tag %q was accepted, want the gate to fail before any build job\n%s", tc.tag, out)
				}
				if !strings.Contains(string(out), "::error::") {
					t.Fatalf("tag %q: rejection did not report an ::error:: annotation:\n%s", tc.tag, out)
				}
				return
			}
			if exitCode != 0 {
				t.Fatalf("tag %q: gate failed, want channel %s\n%s", tc.tag, tc.acceptedAs, out)
			}
			written, err := os.ReadFile(outPath) //nolint:gosec // G304: temp path
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(written), "channel="+tc.wantChannel) {
				t.Fatalf("tag %q: $GITHUB_OUTPUT missing channel=%s: %s", tc.tag, tc.wantChannel, written)
			}
			if !strings.Contains(string(written), "prerelease="+tc.wantPre) {
				t.Fatalf("tag %q: $GITHUB_OUTPUT missing prerelease=%s: %s", tc.tag, tc.wantPre, written)
			}
		})
	}
}

// TestWorkflowWailsBuildHasChannelLdflag pins that every `wails build` invocation in
// all three desktop workflows — darwin/linux and the separate Windows -nsis step —
// injects the paths.buildChannel ldflag, so the produced binary actually reports the
// channel it was built for instead of silently falling back to Dev.
func TestWorkflowWailsBuildHasChannelLdflag(t *testing.T) {
	for _, wf := range []string{"release.yml", "nightly.yml", "manual-build.yml"} {
		t.Run("Given "+wf+" When wails build runs Then every invocation carries the channel ldflag", func(t *testing.T) {
			content := readWorkflow(t, wf)
			found := 0
			for _, line := range strings.Split(content, "\n") {
				if !strings.Contains(line, "wails build") {
					continue
				}
				found++
				if !strings.Contains(line, channelLdflagFragment) {
					t.Fatalf("%s: wails build line lacks the channel ldflag:\n%s", wf, line)
				}
			}
			if found == 0 {
				t.Fatalf("%s: no wails build invocation found", wf)
			}
			// Two build steps per workflow: the non-Windows branch and the -nsis
			// Windows branch. Both must be covered, not just the first one found.
			if found < 2 {
				t.Fatalf("%s: expected the non-Windows and Windows wails build steps, found %d", wf, found)
			}
		})
	}
}

// TestWorkflowChannelPerWorkflow pins which channel each workflow resolves to per the
// task: release.yml derives CHANNEL from the tag-gate job's output (so it can be
// either stable or beta depending on the tag), nightly.yml is pinned to nightly, and
// manual-build.yml is pinned to dev.
func TestWorkflowChannelPerWorkflow(t *testing.T) {
	t.Run("Given release.yml When build-desktop resolves CHANNEL Then it comes from validate-tag's output", func(t *testing.T) {
		content := readWorkflow(t, "release.yml")
		if !strings.Contains(content, "needs.validate-tag.outputs.channel") {
			t.Fatal("release.yml does not consume needs.validate-tag.outputs.channel for CHANNEL")
		}
		if !regexp.MustCompile(`needs:\s*\[\s*validate-tag`).MatchString(content) {
			t.Fatal("release.yml's build-desktop job does not depend on validate-tag, so it could run before the tag is validated")
		}
	})

	t.Run("Given nightly.yml When CHANNEL is set Then it is pinned to nightly", func(t *testing.T) {
		content := readWorkflow(t, "nightly.yml")
		if !regexp.MustCompile(`CHANNEL:\s*nightly\b`).MatchString(content) {
			t.Fatal("nightly.yml does not pin CHANNEL to nightly")
		}
		if strings.Contains(content, "needs.validate-tag") {
			t.Fatal("nightly.yml should not reference a tag-gate job; it always builds nightly")
		}
	})

	t.Run("Given manual-build.yml When CHANNEL is set Then it is pinned to dev", func(t *testing.T) {
		content := readWorkflow(t, "manual-build.yml")
		if !regexp.MustCompile(`CHANNEL:\s*dev\b`).MatchString(content) {
			t.Fatal("manual-build.yml does not pin CHANNEL to dev")
		}
	})

	for _, wf := range []string{"release.yml", "nightly.yml", "manual-build.yml"} {
		t.Run("Given "+wf+" When packaging runs Then it calls scripts/channel-identity.sh", func(t *testing.T) {
			if !strings.Contains(readWorkflow(t, wf), "scripts/channel-identity.sh") {
				t.Fatalf("%s never calls scripts/channel-identity.sh (task 6's shared identity script)", wf)
			}
		})
	}
}

// TestWorkflowPackagingUsesChannelIdentity pins that packaging no longer hard-codes
// the stable channel's identity. The one legitimate literal "build/bin/Agentre.app" is
// the macos-bundle call's *input* path — wails always emits that name before the
// rename (see internal/desktop/channel_build_test.go); every step running after the
// rename (signing, dmg, notarization) must reference the renamed channel bundle
// instead. Linux packaging must not hard-code the agentre command name or desktop
// entry title.
func TestWorkflowPackagingUsesChannelIdentity(t *testing.T) {
	for _, wf := range []string{"release.yml", "nightly.yml", "manual-build.yml"} {
		t.Run("Given "+wf+" When packaging steps run Then they use the channel identity, not hard-coded Agentre", func(t *testing.T) {
			content := readWorkflow(t, wf)

			if got := strings.Count(content, "build/bin/Agentre.app"); got != 1 {
				t.Fatalf("%s: \"build/bin/Agentre.app\" appears %d times, want exactly 1 (the macos-bundle input); every later packaging step must use the renamed channel bundle path", wf, got)
			}
			if !strings.Contains(content, `macos-bundle "build/bin/Agentre.app"`) {
				t.Fatalf("%s: the sole build/bin/Agentre.app reference is not the macos-bundle call", wf)
			}

			if strings.Contains(content, "usr/bin/agentre") {
				t.Fatalf("%s: Linux packaging still hard-codes usr/bin/agentre instead of the channel Linux command", wf)
			}
			if regexp.MustCompile(`Name=Agentre\s*\n`).MatchString(content) {
				t.Fatalf("%s: the .desktop entry still hard-codes Name=Agentre instead of the channel display name", wf)
			}
		})
	}
}
