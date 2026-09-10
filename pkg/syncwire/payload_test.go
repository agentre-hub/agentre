package syncwire_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// apiKeyUnderTest 是 llm_provider 那份用例里的 API Key 取值。取成常量只是为了让
// gosec 的硬编码凭据扫描别在这份契约向量上误报。
const apiKeyUnderTest = "provider-secret-under-test"

// payloadCase 是一种同步对象的载荷契约:一份**每个字段都非零**的取值,以及它与它的
// 零值分别编出来的 JSON。
//
// 两份 golden 各钉一件事,缺一不可:
//
//   - full 钉住**键名与次序**。这些键名是活的线上载荷 —— 它们已经躺在真实账号的
//     sync_objects.payload 里,server 的读路径按键名取值。改一个键名不会有任何编译
//     错误,只会让那一列在每一端静默变空。
//   - zero 钉住 **omitempty 的有无**。omitempty 决定「这个字段没值」在线上是「键不
//     在」还是「键在、值是零」,两者对接收端不是同一件事;PushItem.Payload 少一个
//     omitempty 就把出站队列永久堵死(见 syncwire_test.go 的第一条),那个坑同样能
//     装回到任何一个载荷字段上。
type payloadCase struct {
	kind string
	full any
	// zeroJSON 是该类型零值编出来的 JSON:带 omitempty 的键在这里应当缺席。
	zeroJSON string
	// fullJSON 是 full 编出来的 JSON:每个键名逐字,次序即结构体字段次序。
	fullJSON string
}

func payloadCases() []payloadCase {
	return []payloadCase{
		{
			kind: syncwire.KindProject,
			full: syncwire.ProjectPayload{
				Name: "Atlas", Icon: "rocket", Color: "blue", Description: "the one",
				ParentSyncID: "proj-parent", SortOrder: 7,
			},
			zeroJSON: `{"name":"","icon":"","color":"","description":"","sort_order":0}`,
			fullJSON: `{"name":"Atlas","icon":"rocket","color":"blue","description":"the one",` +
				`"parent_sync_id":"proj-parent","sort_order":7}`,
		},
		{
			kind: syncwire.KindDepartment,
			full: syncwire.DepartmentPayload{
				Name: "Ops", Description: "keeps it up", Icon: "users", AccentColor: "amber",
				ParentSyncID: "dept-parent", LeadAgentSyncID: "agent-lead", SortOrder: 3,
			},
			zeroJSON: `{"name":"","description":"","icon":"","accent_color":"","sort_order":0}`,
			fullJSON: `{"name":"Ops","description":"keeps it up","icon":"users","accent_color":"amber",` +
				`"parent_sync_id":"dept-parent","lead_agent_sync_id":"agent-lead","sort_order":3}`,
		},
		{
			kind: syncwire.KindAgent,
			full: syncwire.AgentPayload{
				Name: "Nova", Description: "ships it", AvatarColor: "violet", AvatarIcon: "bot",
				AvatarHash: "9f86d081884c7d65", SystemBadge: "system",
				DepartmentSyncID: "dept-1", ParentAgentSyncID: "agent-parent",
				SortOrder: 2, PromptJSON: `{"role":"dev"}`, ToolsJSON: `["bash"]`, Pinned: true,
			},
			zeroJSON: `{"name":"","description":"","avatar_color":"","avatar_icon":"",` +
				`"system_badge":"","sort_order":0,"prompt_json":"","tools_json":"","pinned":false}`,
			fullJSON: `{"name":"Nova","description":"ships it","avatar_color":"violet","avatar_icon":"bot",` +
				`"avatar_hash":"9f86d081884c7d65","system_badge":"system","department_sync_id":"dept-1",` +
				`"parent_agent_sync_id":"agent-parent","sort_order":2,"prompt_json":"{\"role\":\"dev\"}",` +
				`"tools_json":"[\"bash\"]","pinned":true}`,
		},
		{
			kind: syncwire.KindAgentBackend,
			full: syncwire.AgentBackendPayload{
				Type: "claudecode", Name: "笔记本上的 Claude", ProviderKey: "anthropic-main",
				ModelKey: "opus", ModelRoutes: `{"fast":"haiku"}`, Sandbox: "workspace-write",
				Approval: "on-request", EnvJSON: `{"HTTP_PROXY":"http://127.0.0.1:7890"}`,
				ReasoningEffort: "high", DefaultPermissionMode: "acceptEdits", DefaultModel: "opus",
				OpenClawGatewayURL: "https://gw.example", OpenClawAgentID: "oc-1",
				OpenClawDefaultModel: "oc-opus", OpenClawSessionMode: "persistent",
			},
			zeroJSON: `{"type":"","name":"","provider_key":"","model_key":"","model_routes":"",` +
				`"sandbox":"","approval":"","env_json":"","reasoning_effort":"",` +
				`"default_permission_mode":"","default_model":"","openclaw_gateway_url":"",` +
				`"openclaw_agent_id":"","openclaw_default_model":"","openclaw_session_mode":""}`,
			fullJSON: `{"type":"claudecode","name":"笔记本上的 Claude","provider_key":"anthropic-main",` +
				`"model_key":"opus","model_routes":"{\"fast\":\"haiku\"}","sandbox":"workspace-write",` +
				`"approval":"on-request","env_json":"{\"HTTP_PROXY\":\"http://127.0.0.1:7890\"}",` +
				`"reasoning_effort":"high","default_permission_mode":"acceptEdits","default_model":"opus",` +
				`"openclaw_gateway_url":"https://gw.example","openclaw_agent_id":"oc-1",` +
				`"openclaw_default_model":"oc-opus","openclaw_session_mode":"persistent"}`,
		},
		{
			kind:     syncwire.KindAgentBackendCLI,
			full:     syncwire.AgentBackendCLIPayload{CLIPath: "/opt/homebrew/bin/claude"},
			zeroJSON: `{"cli_path":""}`,
			fullJSON: `{"cli_path":"/opt/homebrew/bin/claude"}`,
		},
		{
			kind: syncwire.KindAgentExecTarget,
			full: syncwire.AgentExecTargetPayload{
				AgentSyncID: "agent-1", BackendSyncID: "backend-1",
				SortOrder: 1, SkillsJSON: `["review"]`,
			},
			zeroJSON: `{"agent_sync_id":"","backend_sync_id":"","sort_order":0,"skills_json":""}`,
			fullJSON: `{"agent_sync_id":"agent-1","backend_sync_id":"backend-1","sort_order":1,` +
				`"skills_json":"[\"review\"]"}`,
		},
		{
			kind: syncwire.KindProjectAgent,
			full: syncwire.ProjectAgentPayload{
				ProjectSyncID: "proj-1", AgentSyncID: "agent-1", JoinedAt: 1788748996601,
			},
			zeroJSON: `{"project_sync_id":"","agent_sync_id":"","joined_at":0}`,
			fullJSON: `{"project_sync_id":"proj-1","agent_sync_id":"agent-1","joined_at":1788748996601}`,
		},
		{
			kind:     syncwire.KindProjectLocation,
			full:     syncwire.ProjectLocationPayload{Path: "/Users/dev/code/atlas"},
			zeroJSON: `{"path":""}`,
			fullJSON: `{"path":"/Users/dev/code/atlas"}`,
		},
		{
			kind: syncwire.KindLLMProvider,
			full: syncwire.LLMProviderPayload{
				Name: "Anthropic", Type: "anthropic", BaseURL: "https://api.anthropic.com",
				APIKey: apiKeyUnderTest, DefaultModelKey: "opus", Enabled: true,
				Models: []syncwire.LLMProviderModel{{
					ModelKey: "opus", ModelID: "claude-opus-5", Name: "Opus 5",
					Enabled: true, ContextWindow: 200000, MaxOutput: 64000,
				}},
			},
			zeroJSON: `{"name":"","type":"","base_url":"","api_key":"","default_model_key":"",` +
				`"enabled":false,"models":null}`,
			fullJSON: `{"name":"Anthropic","type":"anthropic","base_url":"https://api.anthropic.com",` +
				`"api_key":"` + apiKeyUnderTest + `","default_model_key":"opus","enabled":true,` +
				`"models":[{"model_key":"opus","model_id":"claude-opus-5","name":"Opus 5",` +
				`"enabled":true,"context_window":200000,"max_output":64000}]}`,
		},
		{
			kind:     syncwire.KindLabel,
			full:     syncwire.LabelPayload{Name: "bug", Tone: "red", Status: 1},
			zeroJSON: `{"name":"","tone":"","status":0}`,
			fullJSON: `{"name":"bug","tone":"red","status":1}`,
		},
		{
			kind: syncwire.KindIssue,
			full: syncwire.IssuePayload{
				Title: "Ship it", Description: "why", Stage: "doing", Position: 2.5,
				ProjectSyncID: "proj-1", AgentSyncID: "agent-1", AgentBackendSyncID: "backend-1",
				LLMProviderKey: "anthropic-main", LLMModelKey: "opus", ClosedAt: 1788748996601,
			},
			zeroJSON: `{"title":"","description":"","stage":"","position":0,` +
				`"llm_provider_key":"","llm_model_key":"","closed_at":0}`,
			fullJSON: `{"title":"Ship it","description":"why","stage":"doing","position":2.5,` +
				`"project_sync_id":"proj-1","agent_sync_id":"agent-1","agent_backend_sync_id":"backend-1",` +
				`"llm_provider_key":"anthropic-main","llm_model_key":"opus","closed_at":1788748996601}`,
		},
		{
			kind:     syncwire.KindIssueLabel,
			full:     syncwire.IssueLabelPayload{IssueSyncID: "issue-1", LabelSyncID: "label-1"},
			zeroJSON: `{"issue_sync_id":"","label_sync_id":""}`,
			fullJSON: `{"issue_sync_id":"issue-1","label_sync_id":"label-1"}`,
		},
	}
}

// 键名逐字。这些载荷已经躺在真实账号的 sync_objects.payload 里,server 的读写路径
// 按键名取值 —— 改一个键名编译不出任何错误,只会让那一列在每一端静默变空。
func TestPayloads_GivenAPopulatedValue_ThenEveryJSONKeyIsVerbatim(t *testing.T) {
	t.Parallel()

	for _, c := range payloadCases() {
		t.Run(c.kind, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(c.full)
			require.NoError(t, err)
			require.JSONEq(t, c.fullJSON, string(encoded))
			require.Equal(t, c.fullJSON, string(encoded), "键的次序也是契约的一部分")
		})
	}
}

// omitempty 的有无逐字。它决定「这个字段没值」在线上是「键不在」还是「键在、值是
// 零」——两者对接收端不是同一件事,而 PushItem.Payload 少一个 omitempty 就永久堵死
// 出站队列(syncwire_test.go 的第一条)。零值编出来的这份 JSON 把每个载荷上的这个
// 选择钉住:带 omitempty 的键必须缺席,不带的必须在场。
func TestPayloads_GivenAZeroValue_ThenOnlyOmitemptyKeysAreAbsent(t *testing.T) {
	t.Parallel()

	for _, c := range payloadCases() {
		t.Run(c.kind, func(t *testing.T) {
			t.Parallel()
			zero := reflect.New(reflect.TypeOf(c.full)).Elem().Interface()
			encoded, err := json.Marshal(zero)
			require.NoError(t, err)
			require.Equal(t, c.zeroJSON, string(encoded))
		})
	}
}

// 每一份载荷都过得了自己那一条守卫。这不是重复 guard_test.go:那边钉的是守卫**认
// 什么**,这边钉的是这十二个契约类型**自己**不带守卫要挡的东西 —— 没有本地自增
// ID(跨机引用一律 sync_id / 指纹 / provider_key,全是字符串)、没有头像正文、
// 只有 llm_provider 带 api_key、agent_backend 不带 cli_path。日后往载荷上加字段时,
// 加错形状的那一刻这里就红。
func TestPayloads_GivenEveryContractType_ThenItPassesItsOwnGuard(t *testing.T) {
	t.Parallel()

	for _, c := range payloadCases() {
		t.Run(c.kind, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(c.full)
			require.NoError(t, err)
			require.NoError(t, syncwire.GuardPayload(c.kind, encoded))
		})
	}
}

// 「哪种 kind 对应哪个载荷类型」在整个工作区只有一个答案。
//
// 它从前只散落在桌面端各 adapter 的 kind() 方法里 —— 一个 kind 与一个私有结构体的
// 对应关系,在 sync_svc 之外没有任何地方说得出来,server 因此只能拿字符串字面量读
// 写同一份 JSON。PayloadFor 把它变成契约的一部分:新增一个 kind 却忘了给它载荷
// 类型,这条当场红,而不是等到某一端解出一片空值。
func TestPayloadFor_GivenTheVocabulary_ThenEveryKindHasExactlyOnePayloadType(t *testing.T) {
	t.Parallel()

	for _, kind := range syncwire.Kinds {
		got, ok := syncwire.PayloadFor(kind)
		require.True(t, ok, "kind %s 没有载荷类型", kind)
		require.NotNil(t, got)
		require.Equal(t, reflect.Pointer, reflect.TypeOf(got).Kind(),
			"要能直接交给 json.Unmarshal,拿到的必须是指针")
	}

	_, ok := syncwire.PayloadFor("issue_comment")
	require.False(t, ok, "取值域与 Kinds 一样是闭合的")
}

// PayloadFor 每次给一份**新的**零值:两个调用方各解各的,谁也别把上一次解出来的
// 残留带给下一次。
func TestPayloadFor_GivenTwoCalls_ThenEachGetsItsOwnZeroValue(t *testing.T) {
	t.Parallel()

	first, ok := syncwire.PayloadFor(syncwire.KindProject)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(`{"name":"Atlas"}`), first))
	require.Equal(t, "Atlas", first.(*syncwire.ProjectPayload).Name)

	second, ok := syncwire.PayloadFor(syncwire.KindProject)
	require.True(t, ok)
	require.Empty(t, second.(*syncwire.ProjectPayload).Name)
	require.NotSame(t, first, second)
}

// 契约里的载荷类型不多不少就是词表那十二个:多出一个没有 kind 的载荷类型,说明
// 要么词表漏了一个 kind,要么这个类型根本不该住在契约里。
func TestPayloadFor_GivenThePayloadTypes_ThenTheyMatchTheVocabularyExactly(t *testing.T) {
	t.Parallel()

	require.Len(t, payloadCases(), len(syncwire.Kinds),
		"每个 kind 恰好一条载荷契约用例")

	covered := make(map[string]bool, len(syncwire.Kinds))
	for _, c := range payloadCases() {
		require.True(t, syncwire.KindValid(c.kind), "%s 不在词表里", c.kind)
		require.False(t, covered[c.kind], "%s 有两条用例", c.kind)
		covered[c.kind] = true

		got, ok := syncwire.PayloadFor(c.kind)
		require.True(t, ok)
		require.Equal(t, reflect.TypeOf(c.full), reflect.TypeOf(got).Elem(),
			"PayloadFor(%s) 与用例里的类型必须是同一个", c.kind)
	}
}
