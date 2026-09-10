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
