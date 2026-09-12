package syncwire_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// 墓碑不带正文。Payload 少了 omitempty,json.RawMessage 的零值会编成 JSON `null`,
// 而 null 不是对象 —— server 的 ValidatePayload 拿 root.(map[string]any) 判,直接
// ErrPayloadNotObject 整批拒(30501)。后果不是「这一条没上去」:出站队列按批推进,
// 一次删除就把它**永久堵死**。
//
// 这条性质从前只活在桌面端一份私有结构体的注释里,而服务端那份同名结构没有
// omitempty。两份合成一份时照搬哪一边,决定了这个坑装不装回来。
func TestPushItem_GivenATombstone_ThenThePayloadKeyIsAbsentNotNull(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(syncwire.PushItem{
		Kind: syncwire.KindProject, SyncID: "p-1", DeletedAt: 1788748996601,
	})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"payload"`,
		"墓碑不该带 payload 键;带上 null 会让 server 整批拒并堵死出站队列")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	_, present := decoded["payload"]
	require.False(t, present)
}

// 有正文时,载荷必须原样是一份 JSON 文档,而不是被编成 base64 字符串。
// Payload 若声明成 []byte 而不是 json.RawMessage,encoding/json 就会 base64 它。
func TestPushItem_GivenAPayload_ThenItIsEmbeddedAsRawJSONNotBase64(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(syncwire.PushItem{
		Kind: syncwire.KindProject, SyncID: "p-1",
		Payload: json.RawMessage(`{"name":"demo"}`),
	})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"payload":{"name":"demo"}`)
}

// 上行批量的上限归契约所有。这里只钉住取值本身;它与 server 那条 gin 标签
// (max=500)逐字相符,由 agentre-server 侧的守卫盯着 —— 标签是字面量,引用不了常量,
// 所以那条守卫必须待在标签所在的仓库。
func TestLimits_GivenThePushBatchCap_ThenItIsStatedOnce(t *testing.T) {
	t.Parallel()

	require.Equal(t, 500, syncwire.MaxPushBatch)
	require.Equal(t, 1000, syncwire.MaxPullLimit)
	require.Equal(t, 2000, syncwire.MaxLocalPathItems)
}

// 「同步组里有哪些对象类型」从前有三个答案:这里的常量、桌面端 sync_svc 的
// syncKinds、服务端 sync_entity 的 KindValid —— 常量表虽然只有一份,**成员资格**却
// 由两个宿主各自枚举,任何一边漏掉一个新 kind,那类对象就在那一端整类静默不同步。
// Kinds 是唯一的那份枚举,KindValid 按它判定;两个宿主都只是引用它。
func TestKinds_GivenTheVocabulary_ThenMembershipIsStatedOnce(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		syncwire.KindProject,
		syncwire.KindDepartment,
		syncwire.KindAgent,
		syncwire.KindAgentBackend,
		syncwire.KindAgentBackendCLI,
		syncwire.KindAgentExecTarget,
		syncwire.KindProjectAgent,
		syncwire.KindProjectLocation,
		syncwire.KindLLMProvider,
		syncwire.KindLabel,
		syncwire.KindIssue,
		syncwire.KindIssueLabel,
	}, syncwire.Kinds)

	for _, kind := range syncwire.Kinds {
		require.True(t, syncwire.KindValid(kind), kind)
	}
	require.False(t, syncwire.KindValid("issue_comment"), "取值域是闭合的")
	require.False(t, syncwire.KindValid(""), "空 kind 不属于同步组")
}

// Kinds 的次序是**承重的**:按「被引用者在前」排,遍历全部类型的地方(认领 R12a、
// 出站入队)因此让父行先落地,R2a 的暂缓少绕一圈。这条把关键的几对先后钉住,免得
// 日后追加 kind 时随手贴到末尾就破坏了它。
func TestKinds_GivenTheOrder_ThenReferencedKindsComeFirst(t *testing.T) {
	t.Parallel()

	index := make(map[string]int, len(syncwire.Kinds))
	for i, kind := range syncwire.Kinds {
		index[kind] = i
	}
	for _, pair := range [][2]string{
		{syncwire.KindDepartment, syncwire.KindAgent},
		{syncwire.KindAgentBackend, syncwire.KindAgentBackendCLI},
		{syncwire.KindAgentBackend, syncwire.KindAgentExecTarget},
		{syncwire.KindProject, syncwire.KindProjectAgent},
		{syncwire.KindProject, syncwire.KindProjectLocation},
		{syncwire.KindLabel, syncwire.KindIssueLabel},
		{syncwire.KindIssue, syncwire.KindIssueLabel},
		{syncwire.KindProject, syncwire.KindIssue},
	} {
		require.Less(t, index[pair[0]], index[pair[1]], "%s 必须排在 %s 之前", pair[0], pair[1])
	}
}
