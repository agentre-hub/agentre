package state

import (
	"encoding/json"
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
		// daemon 手里是启动时读到的内存副本，不重新读盘就永远看不到自己已登录。
		convey.Convey("AdoptLoginFromDisk picks up a claim written by another process", func() {
			st, _ := Load(dir)
			require.NoError(t, st.Save())
			require.False(t, st.IsLoggedIn())

			// 另一个进程完成登录。
			other, _ := Load(dir)
			other.Mutate(func(s *State) { s.AccountServerURL = "https://server.example" })
			other.Login("42", AccountCredential{DeviceID: 7, AccessToken: "at", RefreshToken: "rt"})
			require.NoError(t, other.Save())

			adopted, err := st.AdoptLoginFromDisk()
			require.NoError(t, err)
			assert.True(t, adopted, "登录是新出现的，应报告已采纳")
			assert.True(t, st.IsLoggedIn())

			snap := st.Snapshot()
			assert.Equal(t, "42", snap.AccountID)
			assert.Equal(t, "https://server.example", snap.AccountServerURL)
			assert.Equal(t, "at", snap.Credential.AccessToken)
			assert.Equal(t, "rt", snap.Credential.RefreshToken)
			assert.Equal(t, int64(7), snap.Credential.DeviceID)
		})

		convey.Convey("AdoptLoginFromDisk leaves an already-claimed state alone", func() {
			st, _ := Load(dir)
			st.Login("mine", AccountCredential{AccessToken: "mine-at"})
			require.NoError(t, st.Save())

			// 盘上换成了另一个账号（例如 logout + 重新登录留下的残留）。
			other, _ := Load(dir)
			other.Login("theirs", AccountCredential{AccessToken: "theirs-at"})
			require.NoError(t, other.Save())

			adopted, err := st.AdoptLoginFromDisk()
			require.NoError(t, err)
			assert.False(t, adopted, "已登录时不读盘、不覆盖内存里那份")
			assert.Equal(t, "mine", st.Snapshot().AccountID)
			assert.Equal(t, "mine-at", st.Snapshot().Credential.AccessToken)
		})

		convey.Convey("AdoptLoginFromDisk reports no login when disk is still logged out", func() {
			st, _ := Load(dir)
			require.NoError(t, st.Save())
			adopted, err := st.AdoptLoginFromDisk()
			require.NoError(t, err)
			assert.False(t, adopted)
			assert.False(t, st.IsLoggedIn())
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

// H6:升级前的 state.json 还带着本地验签时代的公钥集与吊销列表。升级后的 daemon 必须
// 照常读它 —— 已登录的机器不用重新登录 —— 并在下一次写盘时丢掉这些再没有人读的字段。
func TestState_GivenPreUpgradeStateWithVerificationAndRevocationFields_WhenLoadedAndSaved_ThenKeepsTheLoginAndDropsThem(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"schemaVersion":1,"daemonInstanceUUID":"uuid-1","accountServerURL":"https://a.example",` +
		`"listen":{"lanHost":"0.0.0.0","lanPort":7456},"pairedPeers":{},"llmProviders":{},"preferences":{},` +
		`"accountId":"42","verificationPublicKeyPEM":"pem","verificationCurrentKID":"kid-1",` +
		`"verificationPublicKeys":{"kid-1":"pem"},"maxTokenLifetimeSeconds":900,` +
		`"credential":{"deviceId":7,"accessToken":"at","refreshToken":"rt"},` +
		`"revokedJTIs":["jti-1"],"revocationsAsOf":1700}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o600))

	st, err := Load(dir)
	require.NoError(t, err)
	snap := st.Snapshot()
	assert.Equal(t, "42", snap.AccountID, "an upgraded daemon stays logged in")
	assert.Equal(t, "at", snap.Credential.AccessToken)
	require.NoError(t, st.Save())

	raw, err := os.ReadFile(filepath.Join(dir, "state.json")) //nolint:gosec // G304: dir is this test's t.TempDir.
	require.NoError(t, err)
	var onDisk map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	for _, stale := range []string{
		"verificationPublicKeyPEM", "verificationCurrentKID", "verificationPublicKeys",
		"maxTokenLifetimeSeconds", "revokedJTIs", "revocationsAsOf",
	} {
		assert.NotContains(t, onDisk, stale, "state.json must no longer carry %s", stale)
	}
	assert.Contains(t, onDisk, "accountId")
	assert.Contains(t, onDisk, "credential")
}

// ── Logout 的语义：留下什么，而不是删掉什么 ────────────────────────────────
//
// 老写法逐个列举要清的字段，于是**新加的账号绑定字段默认被留下**——accountServerURL
// 就是这么漏掉的（登录时由 login 写入，logout 从没清过），llmProviders 也是
// （enginesnapshot 从账号拉下来的整份供应商配置，含 API key）。
//
// 这个用例把判据倒过来：只有下面这份「与账号无关的本机状态」允许存活，其余一律
// 归零。往 State 上加字段时，它会逼着你在这里表态——默认答案是「跟着登录一起走」。
func TestLogout_KeepsOnlyMachineLocalState(t *testing.T) {
	survives := map[string]bool{
		"SchemaVersion":      true, // 结构版本，与账号无关
		"DaemonInstanceUUID": true, // 这台机器的身份，LAN 配对的指纹由它派生
		"Listen":             true, // 运行时监听配置
		"PairedPeers":        true, // LAN 配对：R19 说 logout 回到「只有配对」的状态
		"Preferences":        true, // 本机偏好（日志级别、配对码 TTL…）
	}

	before := fullyPopulatedState(t)
	st := fullyPopulatedState(t)
	st.Logout()

	value := reflect.ValueOf(st).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		got := value.Field(i).Interface()
		if survives[field.Name] {
			assert.Equal(t, reflect.ValueOf(before).Elem().Field(i).Interface(), got,
				"%s 是本机状态，logout 不该动它", field.Name)
			continue
		}
		assert.True(t, carriesNothing(value.Field(i)),
			"%s 没有被 logout 清掉。它要么是账号绑定的（那就该清），要么是本机状态"+
				"（那就把它加进上面的 survives 并说明理由）——不要默认留下", field.Name)
	}
}

// 两个具体的回归点，单独守一次：它们是这次真的漏掉的两个字段。
func TestLogout_ClearsTheAccountServerURLAndItsProviderSnapshot(t *testing.T) {
	convey.Convey("logout leaves no trace of the account the daemon just left", t, func() {
		st := fullyPopulatedState(t)
		st.Logout()

		convey.Convey("the account server address goes with the claim", func() {
			// 留着它，`run` 的持久化回退会在 logout 之后把 daemon 又指回旧 server。
			assert.Empty(t, st.AccountServerURL)
		})
		convey.Convey("so does the provider snapshot pulled from that account", func() {
			// enginesnapshot 从账号拉下来的整份配置，含 API key：一台已经离开账号的
			// 机器上不该留着上一个账号的凭证（R19）。
			assert.Empty(t, st.LLMProviders)
		})
		convey.Convey("but the LAN pairings stay: logout returns to the pairing-only state", func() {
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
		s.AccountServerURL = "https://a.example"
		s.Listen = ListenPrefs{LanHost: "0.0.0.0", LanPort: 7456}
		s.PairedPeers = map[string]PairedPeer{"desktop": {DeviceName: "mac", DeviceToken: "t"}}
		s.LLMProviders = map[string]LLMProviderMeta{"p": {Name: "OpenAI", APIKey: "sk-secret"}}
		s.Preferences = Preferences{LogLevel: "info", LogRotateMB: 50}
		s.AccountID = "account-a"
		s.Credential = AccountCredential{DeviceID: 1, AccessToken: "a", RefreshToken: "r"}
		s.DirectCredentials = map[string]DirectCredential{"sha256:desk": {Credential: "c", AccountID: "account-a"}}
	})
	return st
}

// ── 本地直连凭据:按桌面端指纹记账,记下签发时的账号 ─────────────────────────

// D3:第一次为一台桌面端确保凭据时,记下候选值与账号并**原子落盘** —— daemon 重启后
// 桌面端手里那一张必须仍然认得,而写不下来的凭据不该被发出去。
func TestState_EnsureDirectCredential_GivenTheDesktopHoldsNone_WhenEnsured_ThenRecordsTheCandidateUnderTheAccountOnDisk(t *testing.T) {
	dir := setupStateTest(t)
	st, err := Load(dir)
	require.NoError(t, err)
	st.Login("account-a", AccountCredential{AccessToken: "at"})

	credential, issued, err := st.EnsureDirectCredential("sha256:desk", "account-a", "candidate-1")

	require.NoError(t, err)
	assert.True(t, issued)
	assert.Equal(t, "candidate-1", credential)
	reloaded, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]DirectCredential{"sha256:desk": {Credential: "candidate-1", AccountID: "account-a"}},
		reloaded.Snapshot().DirectCredentials)
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "state.json holds credentials and stays owner-only")
}

// D4:同一台桌面端再次握手沿用已有那一张,候选值被丢弃。
func TestState_EnsureDirectCredential_GivenTheDesktopAlreadyHoldsOne_WhenEnsuredAgain_ThenReturnsTheExistingOne(t *testing.T) {
	st, err := Load(setupStateTest(t))
	require.NoError(t, err)
	st.Login("account-a", AccountCredential{AccessToken: "at"})
	_, _, err = st.EnsureDirectCredential("sha256:desk", "account-a", "candidate-1")
	require.NoError(t, err)

	credential, issued, err := st.EnsureDirectCredential("sha256:desk", "account-a", "candidate-2")

	require.NoError(t, err)
	assert.False(t, issued)
	assert.Equal(t, "candidate-1", credential)
}

// 记在别的账号名下的那一张对当前账号不算「已有」:它在 auth.direct 上本来就会被拒。
func TestState_EnsureDirectCredential_GivenTheRecordIsUnderAnotherAccount_WhenEnsured_ThenReplacesIt(t *testing.T) {
	st, err := Load(setupStateTest(t))
	require.NoError(t, err)
	st.Login("account-a", AccountCredential{AccessToken: "at"})
	st.Mutate(func(s *State) {
		s.DirectCredentials = map[string]DirectCredential{"sha256:desk": {Credential: "stale", AccountID: "account-z"}}
	})

	credential, issued, err := st.EnsureDirectCredential("sha256:desk", "account-a", "candidate-1")

	require.NoError(t, err)
	assert.True(t, issued)
	assert.Equal(t, "candidate-1", credential)
	assert.Equal(t, DirectCredential{Credential: "candidate-1", AccountID: "account-a"}, st.Snapshot().DirectCredentials["sha256:desk"])
}

// 核验与记账之间 daemon 可能已经离开了那个账号:此时一张都不记,免得登出后留下残余。
func TestState_EnsureDirectCredential_GivenTheStateNoLongerBelongsToThatAccount_WhenEnsured_ThenRecordsNothing(t *testing.T) {
	for name, login := range map[string]string{"logged out": "", "another account": "account-b"} {
		t.Run(name, func(t *testing.T) {
			st, err := Load(setupStateTest(t))
			require.NoError(t, err)
			if login != "" {
				st.Login(login, AccountCredential{AccessToken: "at"})
			}

			_, _, err = st.EnsureDirectCredential("sha256:desk", "account-a", "candidate-1")

			require.Error(t, err)
			assert.Empty(t, st.Snapshot().DirectCredentials)
		})
	}
}

// 按桌面端指纹删:只删点名的那几台,并落盘。
func TestState_DeleteDirectCredentials_GivenSeveralDesktops_WhenSomeAreDeleted_ThenOnlyThoseAreGoneOnDisk(t *testing.T) {
	dir := setupStateTest(t)
	st, err := Load(dir)
	require.NoError(t, err)
	st.Login("account-a", AccountCredential{AccessToken: "at"})
	for _, fingerprint := range []string{"sha256:desk-1", "sha256:desk-2", "sha256:desk-3"} {
		_, _, err := st.EnsureDirectCredential(fingerprint, "account-a", "credential-"+fingerprint)
		require.NoError(t, err)
	}

	require.NoError(t, st.DeleteDirectCredentials("sha256:desk-1", "sha256:desk-3", "sha256:unknown"))

	reloaded, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]DirectCredential{"sha256:desk-2": {Credential: "credential-sha256:desk-2", AccountID: "account-a"}},
		reloaded.Snapshot().DirectCredentials)
}

// Snapshot 是只读数据袋:改它手里的表不得改到活的状态。
func TestState_Snapshot_GivenDirectCredentials_WhenTheSnapshotIsMutated_ThenTheLiveStateIsUntouched(t *testing.T) {
	st, err := Load(setupStateTest(t))
	require.NoError(t, err)
	st.Login("account-a", AccountCredential{AccessToken: "at"})
	_, _, err = st.EnsureDirectCredential("sha256:desk", "account-a", "candidate-1")
	require.NoError(t, err)

	snapshot := st.Snapshot()
	delete(snapshot.DirectCredentials, "sha256:desk")

	assert.Contains(t, st.Snapshot().DirectCredentials, "sha256:desk")
}
