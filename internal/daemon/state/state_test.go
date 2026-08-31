package state

import (
	"os"
	"path/filepath"
	"reflect"
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

// ── Unclaim 的语义：留下什么，而不是删掉什么 ────────────────────────────────
//
// 老写法逐个列举要清的字段，于是**新加的账号绑定字段默认被留下**——hubServerURL
// 就是这么漏掉的（认领时由 login 写入，unclaim 从没清过），llmProviders 也是
// （enginesnapshot 从账号拉下来的整份供应商配置，含 API key）。
//
// 这个用例把判据倒过来：只有下面这份「与账号无关的本机状态」允许存活，其余一律
// 归零。往 State 上加字段时，它会逼着你在这里表态——默认答案是「跟着认领一起走」。
func TestUnclaim_KeepsOnlyMachineLocalState(t *testing.T) {
	survives := map[string]bool{
		"SchemaVersion":      true, // 结构版本，与账号无关
		"DaemonInstanceUUID": true, // 这台机器的身份，LAN 配对的指纹由它派生
		"Listen":             true, // 运行时监听配置
		"PairedPeers":        true, // LAN 配对：R19 说 unclaim 回到「只有配对」的状态
		"Preferences":        true, // 本机偏好（日志级别、配对码 TTL…）
	}

	before := fullyPopulatedState(t)
	st := fullyPopulatedState(t)
	st.Unclaim()

	value := reflect.ValueOf(st).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		got := value.Field(i).Interface()
		if survives[field.Name] {
			assert.Equal(t, reflect.ValueOf(before).Elem().Field(i).Interface(), got,
				"%s 是本机状态，unclaim 不该动它", field.Name)
			continue
		}
		assert.True(t, carriesNothing(value.Field(i)),
			"%s 没有被 unclaim 清掉。它要么是账号绑定的（那就该清），要么是本机状态"+
				"（那就把它加进上面的 survives 并说明理由）——不要默认留下", field.Name)
	}
}

// 两个具体的回归点，单独守一次：它们是这次真的漏掉的两个字段。
func TestUnclaim_ClearsTheAccountServerURLAndItsProviderSnapshot(t *testing.T) {
	convey.Convey("unclaim leaves no trace of the account the daemon just left", t, func() {
		st := fullyPopulatedState(t)
		st.Unclaim()

		convey.Convey("the account server address goes with the claim", func() {
			// 留着它，`run` 的持久化回退会在 unclaim 之后把 daemon 又指回旧 server。
			assert.Empty(t, st.HubServerURL)
		})
		convey.Convey("so does the provider snapshot pulled from that account", func() {
			// enginesnapshot 从账号拉下来的整份配置，含 API key：一台已经离开账号的
			// 机器上不该留着上一个账号的凭证（与 revokedJTIs 同一条理由，R19）。
			assert.Empty(t, st.LLMProviders)
		})
		convey.Convey("but the LAN pairings stay: unclaim returns to the pairing-only state", func() {
			assert.Len(t, st.PairedPeers, 1)
		})
	})
}

// carriesNothing 判「这个字段不带任何内容」。map / slice 看长度而不是零值：清空后的
// 表要保持非 nil（Load 保证这一条，daemon 侧的写入方直接往里赋值），非 nil 的空表
// 不是零值，却确实什么都没带。
func carriesNothing(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Map, reflect.Slice:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

// fullyPopulatedState 造一份**每个字段都非零**的 state：这样「清掉了」与「本来就是
// 零值」不会混为一谈。
func fullyPopulatedState(t *testing.T) *State {
	t.Helper()
	st, err := Load(t.TempDir())
	require.NoError(t, err)
	st.Mutate(func(s *State) {
		s.SchemaVersion = CurrentSchemaVersion
		s.DaemonInstanceUUID = "uuid-1"
		s.HubServerURL = "https://a.example"
		s.Listen = ListenPrefs{LanHost: "0.0.0.0", LanPort: 7456}
		s.PairedPeers = map[string]PairedPeer{"desktop": {DeviceName: "mac", DeviceToken: "t"}}
		s.LLMProviders = map[string]LLMProviderMeta{"p": {Name: "OpenAI", APIKey: "sk-secret"}}
		s.Preferences = Preferences{LogLevel: "info", LogRotateMB: 50}
		s.AccountID = "account-a"
		s.VerificationPublicKeyPEM = "pem"
		s.VerificationCurrentKID = "kid-1"
		s.VerificationPublicKeys = map[string]string{"kid-1": "pem"}
		s.MaxTokenLifetimeSeconds = 3600
		s.Credential = AccountCredential{DeviceID: 1, AccessToken: "a", RefreshToken: "r"}
		s.RevokedJTIs = []string{"jti-1"}
		s.RevocationsAsOf = 1700
	})
	return st
}
