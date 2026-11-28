package syncwire_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/syncwire/dispatch"
)

// Given 一台设备探到的全部情况,When 编成上报给 server 的 JSON,
// Then 每一类事实都按契约的键名出现,server 解回来的与发出的相同。
func TestDeviceFactsReport_GivenAFullReport_WhenEncoded_ThenItRoundTripsWithContractKeys(t *testing.T) {
	report := syncwire.DeviceFactsReport{
		CollectedAt: 1700000000000,
		Runtimes: []syncwire.RuntimeFact{
			{Runtime: "claudecode", Found: true, Version: "2.1.3", Path: "/usr/local/bin/claude", NativeLogin: syncwire.NativeLoginLoggedIn},
			{Runtime: "antigravity", Found: true, Version: "1.4.0", MinimumVersion: "1.5", BelowMinimum: true},
			{Runtime: "piagent"},
		},
		ACPEngines: []syncwire.ACPEngineFact{{EngineSyncID: "eng-acp", Resolvable: true}},
		ServiceEngines: []syncwire.ServiceEngineFact{
			{EngineSyncID: "eng-hermes", Runtime: "hermes", CredentialPresent: true, LastConnection: syncwire.ConnectionFailed, LastConnectionAt: 1699999999000},
			{EngineSyncID: "eng-openclaw", Runtime: "openclaw", LastConnection: syncwire.ConnectionNever},
		},
		SkillPacks: []syncwire.RuntimeSkillPacks{{Runtime: "claudecode", PackIDs: []string{"superpowers@claude-plugins-official"}}},
		LocalPaths: []syncwire.LocalPathItem{{ProjectSyncID: "p-1", Path: "/work/p1"}},
	}

	raw, err := json.Marshal(report)
	require.NoError(t, err)

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &keys))
	for _, key := range []string{"collected_at", "runtimes", "acp_engines", "service_engines", "skill_packs", "local_paths"} {
		assert.Contains(t, keys, key)
	}
	assert.Contains(t, string(raw), `"native_login":"loggedIn"`)
	assert.Contains(t, string(raw), `"below_minimum":true`)
	assert.Contains(t, string(raw), `"credential_present":true`)
	assert.Contains(t, string(raw), `"last_connection":"failed","last_connection_at":1699999999000`)
	assert.Contains(t, string(raw), `"last_connection":"never"`)

	var back syncwire.DeviceFactsReport
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, report, back)
}

// 上报的去处是一个固定路径:server 那一侧照这个常量挂路由,两边不各写一份字面量。
func TestDeviceFactsReport_EndpointIsAVersionedServerPath(t *testing.T) {
	assert.Equal(t, "/v1/devices/facts", syncwire.PathDeviceFacts)
}

// 上次连接的三个取值是契约里的字面量:server 与控制台按它们显示。
func TestDeviceFactsReport_LastConnectionVocabulary(t *testing.T) {
	assert.Equal(t, "succeeded", syncwire.ConnectionSucceeded)
	assert.Equal(t, "failed", syncwire.ConnectionFailed)
	assert.Equal(t, "never", syncwire.ConnectionNever)
}

// 登录态的三个取值与共享派发函数读的是同一套词:设备报上来的值原样交给 dispatch,
// 不经过任何翻译表。
func TestDeviceFactsReport_NativeLoginVocabularyMatchesDispatch(t *testing.T) {
	assert.Equal(t, string(dispatch.LoginLoggedIn), syncwire.NativeLoginLoggedIn)
	assert.Equal(t, string(dispatch.LoginLoggedOut), syncwire.NativeLoginLoggedOut)
	assert.Equal(t, string(dispatch.LoginUnknown), syncwire.NativeLoginUnknown)
}

// secretWords 是「名字在说它装着一枚秘密」的词根(归一化后的子串)。
var secretWords = []string{"apikey", "token", "secret", "password", "passwd", "credential", "cookie", "privatekey", "bearer", "env", "auth"}

// Given 设备情况上报会发给 server 并按设备持久化、在控制台与其它桌面端上展示,
// When 有人往上报结构里加一个字段,
// Then 名字像秘密的字段只能是布尔值(只说「有没有」),且整棵结构里没有 map ——
// 一个字符串 / 字节 / 任意值字段叫 token / env / credential,或一张自由键值表,
// 就是一条把凭据、token 或环境变量值带出设备的通路(规格「上报的内容」最后一句)。
func TestDeviceFactsReport_GivenEveryField_ThenNoneIsSecretShaped(t *testing.T) {
	assert.Empty(t, secretShapedFields(reflect.TypeOf(syncwire.DeviceFactsReport{}), "DeviceFactsReport"))
}

// 守卫本身要能红:一个装着 token 字符串的结构必须被它认出来,否则上面那条全绿只说明
// 守卫是瞎的。
func TestDeviceFactsGuard_GivenASecretCarryingShape_ThenItIsFlagged(t *testing.T) {
	type leaky struct {
		Runtime string            `json:"runtime"`
		Token   string            `json:"gateway_token"`
		Env     map[string]string `json:"env"`
		HasAuth bool              `json:"has_auth"`
	}
	flagged := secretShapedFields(reflect.TypeOf(leaky{}), "leaky")
	assert.Contains(t, flagged, "leaky.gateway_token")
	assert.Contains(t, flagged, "leaky.env")
	assert.NotContains(t, flagged, "leaky.has_auth")
	assert.NotContains(t, flagged, "leaky.runtime")
}

// secretShapedFields 列出结构树里每一个「名字像秘密却不是布尔」的字段与每一个 map 字段。
// 字段的 JSON 键名与 Go 名都要看:两者任一在说「秘密」都算。
func secretShapedFields(root reflect.Type, rootPath string) []string {
	var flagged []string
	seen := map[reflect.Type]bool{}
	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for i := range typ.NumField() {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			fieldPath := path + "." + name
			if field.Type.Kind() == reflect.Map {
				flagged = append(flagged, fieldPath)
				continue
			}
			if field.Type.Kind() != reflect.Bool && (namesSecret(name) || namesSecret(field.Name)) {
				flagged = append(flagged, fieldPath)
			}
			walk(field.Type, fieldPath)
		}
	}
	walk(root, rootPath)
	return flagged
}

func namesSecret(name string) bool {
	norm := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	for _, word := range secretWords {
		if strings.Contains(norm, word) {
			return true
		}
	}
	return false
}
