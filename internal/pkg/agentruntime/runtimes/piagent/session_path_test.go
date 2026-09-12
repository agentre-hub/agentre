package piagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgpiagent "github.com/agentre-hub/agentre/pkg/piagent"
)

func TestSessionFactoryUsesPiNativeSessionIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake Pi is not portable to windows")
	}
	tests := []struct {
		name              string
		providerSessionID string
		stateSessionID    string
		wantSessionArg    bool
	}{
		{name: "new chat lets Pi create its native session", stateSessionID: "pi-native-new"},
		{name: "existing chat resumes by full native ID", providerSessionID: "pi-native-existing", stateSessionID: "pi-native-existing", wantSessionArg: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args.txt")
			binary := writeFakePiRPC(t, dir)
			sess, err := sessionFactory(agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypePiAgent),
					CLIPath: binary,
				},
				SessionID:         77,
				ProviderSessionID: tt.providerSessionID,
			}, map[string]string{
				"AGENTRE_TEST_PI_ARGS":       argsFile,
				"AGENTRE_TEST_PI_SESSION_ID": tt.stateSessionID,
			}, dir)
			require.NoError(t, err)

			stream, err := sess.Stream(context.Background(), "hello", "", nil)
			require.NoError(t, err)
			for stream.Next() {
			}
			require.NoError(t, stream.Err())
			require.NoError(t, sess.Close(context.Background()))

			argsRaw, err := os.ReadFile(argsFile) //nolint:gosec // test-owned temp path
			require.NoError(t, err)
			args := strings.Split(strings.TrimSpace(string(argsRaw)), "\n")
			assert.Contains(t, args, "--mode")
			assert.Contains(t, args, "rpc")
			assert.NotContains(t, args, "--session-dir")
			assert.NotContains(t, string(argsRaw), "agentre-77.jsonl")
			if tt.wantSessionArg {
				require.Contains(t, args, "--session")
				idx := indexOf(args, "--session")
				require.Less(t, idx+1, len(args))
				assert.Equal(t, tt.providerSessionID, args[idx+1])
			} else {
				assert.NotContains(t, args, "--session")
			}
			assert.Equal(t, tt.stateSessionID, sess.ID())
		})
	}
}

func TestSessionFactoryClassifiesMissingNativeSessionProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake Pi is not portable to windows")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "pi")
	require.NoError(t, os.WriteFile(binary, []byte(`#!/bin/sh
echo "No session found matching 'pi-native-gone'" >&2
exit 1
`), 0o755))
	sess, err := sessionFactory(agentruntime.RunRequest{
		Backend: &agent_backend_entity.AgentBackend{
			Type:    string(agent_backend_entity.TypePiAgent),
			CLIPath: binary,
		},
		ProviderSessionID: "pi-native-gone",
	}, nil, dir)
	require.NoError(t, err)

	stream, err := sess.Stream(context.Background(), "must not run", "", nil)

	assert.Nil(t, stream)
	assert.True(t, errors.Is(err, pkgpiagent.ErrSessionNotFound), "error: %v", err)
}

func writeFakePiRPC(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "pi")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$AGENTRE_TEST_PI_ARGS"
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_state"'*)
      printf '{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"%s"}}\n' "$AGENTRE_TEST_PI_SESSION_ID"
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"agent_end","messages":[],"willRetry":false}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{}}'
      ;;
  esac
done
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}
