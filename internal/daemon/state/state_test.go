package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupStateTest(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestStateLoadSave(t *testing.T) {
	convey.Convey("Load/Save state.json", t, func() {
		dir := setupStateTest(t)

		convey.Convey("when state.json absent, Load returns default + writes file", func() {
			st, err := Load(dir)
			require.NoError(t, err)
			assert.NotEmpty(t, st.DaemonInstanceUUID)
			assert.Equal(t, 1, st.SchemaVersion)
			_, err = os.Stat(filepath.Join(dir, "state.json"))
			assert.NoError(t, err)
		})

		convey.Convey("when state.json present, Load reuses persisted UUID", func() {
			st1, _ := Load(dir)
			uuid1 := st1.DaemonInstanceUUID
			st2, err := Load(dir)
			require.NoError(t, err)
			assert.Equal(t, uuid1, st2.DaemonInstanceUUID,
				"daemonInstanceUUID must be stable across boots")
		})

		convey.Convey("Save writes atomically and is readable back", func() {
			st, _ := Load(dir)
			st.Mutate(func(s *State) {
				s.PairedPeers["sha256:x"] = PairedPeer{
					DeviceName: "foo", DeviceToken: "t", PairedAt: 1, LastSeenAt: 1,
				}
			})
			require.NoError(t, st.Save())

			st2, _ := Load(dir)
			peer, ok := st2.PairedPeers["sha256:x"]
			require.True(t, ok)
			assert.Equal(t, "foo", peer.DeviceName)
		})

		convey.Convey("Schema version mismatch is an error (no auto-migrate)", func() {
			path := filepath.Join(dir, "state.json")
			require.NoError(t, os.WriteFile(path, []byte(`{"schemaVersion":99}`), 0o600))
			_, err := Load(dir)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "schemaVersion")
		})

		convey.Convey("Concurrent Mutate calls are race-free", func() {
			st, _ := Load(dir)
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					st.Mutate(func(s *State) {
						s.LLMProviders[string(rune('a'+i%26))] = LLMProviderMeta{Name: "x"}
					})
				}(i)
			}
			wg.Wait()
			assert.NotEmpty(t, st.LLMProviders)
		})

		convey.Convey("Atomic write: partial write does not corrupt", func() {
			st, _ := Load(dir)
			require.NoError(t, st.Save())
			info, err := os.Stat(filepath.Join(dir, "state.json"))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		})

		convey.Convey("Given a complete engine snapshot, replacing providers deletes absent keys and persists the new catalog", func() {
			st, _ := Load(dir)
			st.Mutate(func(s *State) {
				s.LLMProviders["removed"] = LLMProviderMeta{Name: "old", APIKey: "old-key"}
			})
			require.NoError(t, st.Save())

			replacement := map[string]LLMProviderMeta{
				"provider-1": {
					Name: "Anthropic", Type: "anthropic", BaseURL: "https://api.example", APIKey: "new-key",
					DefaultModelKey: "model-1",
					Models:          []LLMModelMeta{{ModelKey: "model-1", ModelID: "claude-1", Name: "Claude", Enabled: true}},
				},
			}
			require.NoError(t, st.ReplaceLLMProviders(replacement))

			assert.Equal(t, replacement, st.Snapshot().LLMProviders)
			reloaded, err := Load(dir)
			require.NoError(t, err)
			assert.Equal(t, replacement, reloaded.LLMProviders)
			assert.NotContains(t, reloaded.LLMProviders, "removed")
		})

		convey.Convey("Given state.json cannot be replaced, replacing providers keeps the previous in-memory and on-disk map", func() {
			st, _ := Load(dir)
			previous := map[string]LLMProviderMeta{"provider-old": {Name: "Old", APIKey: "old-key"}}
			st.Mutate(func(s *State) { s.LLMProviders = previous })
			require.NoError(t, st.Save())
			require.NoError(t, os.Mkdir(filepath.Join(dir, "state.json.tmp"), 0o700))

			err := st.ReplaceLLMProviders(map[string]LLMProviderMeta{"provider-new": {Name: "New", APIKey: "new-key"}})
			require.Error(t, err)
			assert.Equal(t, previous, st.Snapshot().LLMProviders)

			reloaded, loadErr := Load(dir)
			require.NoError(t, loadErr)
			assert.Equal(t, previous, reloaded.LLMProviders)
		})

		// `agentred login` 是另一个进程：它把凭据写进 state.json 就退出。运行中的
		// daemon 手里是启动时读到的内存副本，不重新读盘就永远看不到自己已被认领。
		convey.Convey("AdoptClaimFromDisk picks up a claim written by another process", func() {
			st, _ := Load(dir)
			require.NoError(t, st.Save())
			require.False(t, st.IsClaimed())

			// 另一个进程完成登录。
			other, _ := Load(dir)
			other.Mutate(func(s *State) { s.HubServerURL = "https://server.example" })
			other.ClaimWithKeySet("42", "kid-1", map[string]string{"kid-1": "PEM"}, 900,
				AccountCredential{DeviceID: 7, AccessToken: "at", RefreshToken: "rt"})
			require.NoError(t, other.Save())

			adopted, err := st.AdoptClaimFromDisk()
			require.NoError(t, err)
			assert.True(t, adopted, "认领是新出现的，应报告已采纳")
			assert.True(t, st.IsClaimed())

			snap := st.Snapshot()
			assert.Equal(t, "42", snap.AccountID)
			assert.Equal(t, "https://server.example", snap.HubServerURL)
			assert.Equal(t, "at", snap.Credential.AccessToken)
			assert.Equal(t, "rt", snap.Credential.RefreshToken)
			assert.Equal(t, int64(7), snap.Credential.DeviceID)
			assert.Equal(t, "kid-1", snap.VerificationCurrentKID)
			assert.Equal(t, "PEM", snap.VerificationPublicKeys["kid-1"])
			assert.Equal(t, int64(900), snap.MaxTokenLifetimeSeconds)
		})

		convey.Convey("AdoptClaimFromDisk leaves an already-claimed state alone", func() {
			st, _ := Load(dir)
			st.Claim("mine", "PEM-mine", AccountCredential{AccessToken: "mine-at"})
			require.NoError(t, st.Save())

			// 盘上换成了另一个账号（例如 unclaim + 重新登录留下的残留）。
			other, _ := Load(dir)
			other.Claim("theirs", "PEM-theirs", AccountCredential{AccessToken: "theirs-at"})
			require.NoError(t, other.Save())

			adopted, err := st.AdoptClaimFromDisk()
			require.NoError(t, err)
			assert.False(t, adopted, "已认领时不读盘、不覆盖内存里那份")
			assert.Equal(t, "mine", st.Snapshot().AccountID)
			assert.Equal(t, "mine-at", st.Snapshot().Credential.AccessToken)
		})

		convey.Convey("AdoptClaimFromDisk reports no claim when disk is still unclaimed", func() {
			st, _ := Load(dir)
			require.NoError(t, st.Save())
			adopted, err := st.AdoptClaimFromDisk()
			require.NoError(t, err)
			assert.False(t, adopted)
			assert.False(t, st.IsClaimed())
		})

		convey.Convey("Snapshot returns an independent copy of maps", func() {
			st, _ := Load(dir)
			st.Mutate(func(s *State) {
				s.LLMProviders["a"] = LLMProviderMeta{Name: "orig"}
			})
			snap := st.Snapshot()
			snap.LLMProviders["a"] = LLMProviderMeta{Name: "changed"}
			// Live state unchanged.
			assert.Equal(t, "orig", st.LLMProviders["a"].Name)
		})
	})
}
