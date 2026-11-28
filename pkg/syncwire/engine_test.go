package syncwire_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// Given agentred 拉到的一份引擎快照,When 编成 JSON,Then 供应商只有元数据与「谁能连到」、
// 引擎配置带类型与 config、CLI 路径按运行时给出 —— 整份里没有 api_key 这个键。
func TestEngineSnapshot_GivenAFullSnapshot_WhenEncoded_ThenItCarriesMetadataButNoKey(t *testing.T) {
	snapshot := syncwire.EngineSnapshot{
		Providers: []syncwire.SnapshotProvider{{
			ProviderKey: "pk-1", Name: "Anthropic", Type: "anthropic", BaseURL: "https://api.anthropic.com",
			DefaultModelKey: "mk-1",
			Models:          []syncwire.LLMProviderModel{{ModelKey: "mk-1", ModelID: "claude-opus", Name: "Opus", Enabled: true, ContextWindow: 200000}},
			Reach:           &syncwire.LLMProviderReach{Mode: "devices", Devices: []string{"fp-a"}},
		}},
		EngineProfiles: []syncwire.SnapshotEngineProfile{{
			SyncID: "eng-1",
			EngineProfilePayload: syncwire.EngineProfilePayload{
				Type: "acp", Name: "Gemini ACP", ProviderKey: "pk-1", ModelKey: "mk-1", ReasoningEffort: "high",
				Config: syncwire.AgentBackendConfig{ACPCommand: "gemini", ACPArgs: []string{"--acp"}},
			},
		}},
		CLIPaths: []syncwire.SnapshotCLIPath{{Runtime: "claudecode", CLIPath: "/opt/claude"}},
	}

	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(raw)), "api_key")
	assert.NotContains(t, strings.ToLower(string(raw)), "apikey")
	for _, fragment := range []string{
		`"providers":[`, `"engine_profiles":[`, `"cli_paths":[`,
		`"provider_key":"pk-1"`, `"base_url":"https://api.anthropic.com"`, `"default_model_key":"mk-1"`,
		`"reach":{"mode":"devices","devices":["fp-a"]}`,
		`"sync_id":"eng-1"`, `"type":"acp"`, `"acpCommand":"gemini"`,
		`"runtime":"claudecode","cli_path":"/opt/claude"`,
	} {
		assert.Contains(t, string(raw), fragment)
	}

	var back syncwire.EngineSnapshot
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, snapshot, back)
}

// 快照的每一层结构里都没有一个 JSON 键名是 api_key —— 不是「这次没填」,是形状里就没有。
func TestEngineSnapshot_GivenEveryField_ThenNoneIsAnAPIKey(t *testing.T) {
	assert.Empty(t, apiKeyFields(reflect.TypeOf(syncwire.EngineSnapshot{}), "EngineSnapshot"))
}

// 守卫本身要能红:形状里藏着 api_key 的结构必须被认出来。
func TestEngineSnapshotGuard_GivenAKeyCarryingShape_ThenItIsFlagged(t *testing.T) {
	type leakyProvider struct {
		Name string `json:"name"`
		Key  string `json:"apiKey"`
	}
	type leaky struct {
		Providers []leakyProvider `json:"providers"`
	}
	assert.Equal(t, []string{"leaky.providers.apiKey"}, apiKeyFields(reflect.TypeOf(leaky{}), "leaky"))
}

// 租约请求与应答的键名就是契约:server 按它们绑定与回复。
func TestLease_GivenARequestAndAResponse_WhenEncoded_ThenTheContractKeysAppear(t *testing.T) {
	request, err := json.Marshal(syncwire.LeaseRequest{ProviderKey: "pk-1", Purpose: syncwire.LeasePurposeTurn})
	require.NoError(t, err)
	assert.JSONEq(t, `{"provider_key":"pk-1","purpose":"turn"}`, string(request))

	response, err := json.Marshal(syncwire.LeaseResponse{
		ProviderKey: "pk-1", APIKey: "sk-test", BaseURL: "https://api.example", ExpiresAt: 1700000000000,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"provider_key":"pk-1","api_key":"sk-test","base_url":"https://api.example","expires_at":1700000000000}`, string(response))
}

// 申请用途是一个闭合词表:转发一轮对话、供应商测试、模型发现。
func TestLease_PurposeVocabulary(t *testing.T) {
	assert.Equal(t, "turn", syncwire.LeasePurposeTurn)
	assert.Equal(t, "test", syncwire.LeasePurposeTest)
	assert.Equal(t, "discover", syncwire.LeasePurposeDiscover)
	for _, purpose := range []string{"turn", "test", "discover"} {
		assert.True(t, syncwire.LeasePurposeValid(purpose), purpose)
	}
	assert.False(t, syncwire.LeasePurposeValid(""))
	assert.False(t, syncwire.LeasePurposeValid("sync"))
}

// 快照、租约两个端点与吊销信号种类各只有一个常量,server 照它挂路由、发信号。
func TestEngineEndpointsAndRevokeSignal(t *testing.T) {
	assert.Equal(t, "/v1/engine/snapshot", syncwire.PathEngineSnapshot)
	assert.Equal(t, "/v1/engine/leases", syncwire.PathProviderLease)
	assert.Equal(t, "lease_revoked", syncwire.AccountSignalLeaseRevoked)
}

// Given 本 module 的全部线上结构,Then 键名是 api_key 的字段只有两处:发给桌面端的
// llm_provider 同步载荷,和发给 agentred 的租约应答。新加一个带 key 的类型 = 这条红,
// 必须先在这里明写它为什么可以带。
func TestSyncwire_GivenEveryType_ThenOnlyTheProviderPayloadAndTheLeaseResponseCarryAnAPIKey(t *testing.T) {
	assert.Equal(t, []string{"LLMProviderPayload.APIKey", "LeaseResponse.APIKey"}, apiKeyFieldsInDir(t, "."))
}

// 守卫本身要能红:源码里一个新结构带 api_key 必须被扫出来,哪怕它藏在嵌套里。
func TestSyncwireKeyGuard_GivenASourceWithAKeyCarryingType_ThenItIsFlagged(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\ntype Snapshot struct {\n\tProviders []struct {\n\t\tSecret string `json:\"api_key\"`\n\t} `json:\"providers\"`\n\tName string `json:\"name\"`\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600))
	assert.Equal(t, []string{"Snapshot.Secret"}, apiKeyFieldsInDir(t, dir))
}

func isAPIKeyName(name string) bool {
	norm := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	return norm == "apikey"
}

// apiKeyFields 列出结构树里每一个 JSON 键名(或 Go 名)是 api_key 的字段。
func apiKeyFields(root reflect.Type, rootPath string) []string {
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
			fieldPath := path
			if !field.Anonymous || name != "" {
				if name == "" {
					name = field.Name
				}
				fieldPath = path + "." + name
				if isAPIKeyName(name) || isAPIKeyName(field.Name) {
					flagged = append(flagged, fieldPath)
				}
			}
			walk(field.Type, fieldPath)
		}
	}
	walk(root, rootPath)
	return flagged
}

// apiKeyFieldsInDir 解析目录里全部非测试 Go 源码,按「外层类型名.字段名」列出 JSON 键名
// 是 api_key 的结构字段。按源码而不是反射扫,是因为反射列不出一个包里有哪些类型。
func apiKeyFieldsInDir(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	var flagged []string
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				ast.Inspect(typeSpec.Type, func(node ast.Node) bool {
					field, ok := node.(*ast.Field)
					if !ok || field.Tag == nil {
						return true
					}
					tag, err := strconv.Unquote(field.Tag.Value)
					if err != nil {
						return true
					}
					jsonName := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
					if isAPIKeyName(jsonName) {
						for _, name := range field.Names {
							flagged = append(flagged, typeSpec.Name.Name+"."+name.Name)
						}
					}
					return true
				})
			}
		}
	}
	sort.Strings(flagged)
	return flagged
}
