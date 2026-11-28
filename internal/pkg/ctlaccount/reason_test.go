package ctlaccount

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 每个派发原因码都有一句给人看的话，且带上与该码相关的参数；没有码时为空。
func TestReasonText(t *testing.T) {
	seen := time.Date(2026, 11, 28, 9, 30, 0, 0, time.UTC).UnixMilli()
	cases := []struct {
		name string
		r    Reason
		want string
	}{
		{"offline with last seen", Reason{Code: "offline", LastSeen: seen}, "offline (last seen 2026-11-28"},
		{"offline never seen", Reason{Code: "offline"}, "offline"},
		{"app not running", Reason{Code: "app_not_running"}, "Agentre is not running on that computer"},
		{"not paired", Reason{Code: "not_paired"}, "this computer is not paired with it"},
		{"label missing", Reason{Code: "label_missing", Labels: []string{"gpu", "linux"}}, "lacks the label gpu, linux"},
		{"not in devices", Reason{Code: "not_in_devices"}, "not one of the devices the agent is pinned to"},
		{"desktop only", Reason{Code: "runtime_desktop_only", Runtime: "openclaw"}, "openclaw only runs on a desktop"},
		{"runtime missing", Reason{Code: "runtime_missing", Runtime: "codex"}, "codex is not installed"},
		{"runtime too old", Reason{Code: "runtime_too_old", Runtime: "codex"}, "codex is too old"},
		{"not logged in", Reason{Code: "not_logged_in", Runtime: "claudecode"}, "claudecode is not logged in"},
		{"provider unreachable", Reason{Code: "provider_unreachable", Provider: "ollama"}, "provider ollama cannot be reached from that device"},
		{"no credential", Reason{Code: "no_credential", Engine: "hermes-main"}, "no credential for hermes-main on that device"},
		{"no project path", Reason{Code: "no_project_path", Project: "agentre"}, "no path for project agentre"},
		{"unknown code is shown as is", Reason{Code: "something_new"}, "something_new"},
		{"no code", Reason{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.r.Text()
			if c.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.True(t, strings.HasPrefix(got, c.want), "%q should start with %q", got, c.want)
		})
	}
}
