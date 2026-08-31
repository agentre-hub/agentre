package remote

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// convOf 是测试里「这条本地会话在线上叫什么」的那一个值 —— 用例通过
// WithConversationIDResolver 把它注入给 Runtime,正如生产里 chat_svc 注入一条读
// chat_sessions.conversation_id 的翻译(见 chat_svc/remote_pool.go)。取值形态无所谓,
// 只要是个合法且逐会话唯一的 uuid;用例断言的是**出线的就是注入进来的那一个**。
func convOf(sid int64) string {
	return conversationid.Derive(conversationid.Namespace, "", strconv.FormatInt(sid, 10))
}

// Given 一条本进程从没登记过的会话号,When 问它的对话身份,Then 得到装配方交给它
// 的那一个(生产上就是 chat_sessions.conversation_id 那一列),且反向翻得回同一个
// 会话号。
func TestConversationID_GivenAnUnknownSession_ThenAsksTheInjectedResolver(t *testing.T) {
	rt := New(newFakeConn(), WithConversationIDResolver(convOf))
	t.Cleanup(func() { _ = rt.Close() })

	got := rt.conversationID(42)
	assert.Equal(t, convOf(42), got)
	require.NoError(t, conversationid.Validate(got))
	assert.Equal(t, int64(42), rt.localSessionID(got))
}

// Given 装配方**没有**注入那条翻译,When 问一条会话的身份,Then 交回空串 ——
// 没人能说出它在线上叫什么时如实报告,而不是就地编一个(编出来的与库里那一列对不
// 上,推送再也落不回这条会话,而且不会有任何症状)。
func TestConversationID_GivenNoResolver_ThenReportsEmptyInsteadOfInventingOne(t *testing.T) {
	rt := New(newFakeConn())
	t.Cleanup(func() { _ = rt.Close() })

	assert.Empty(t, rt.conversationID(42))
}

// Given 未知的对话身份,When 反向翻译,Then 交回 0 —— 调用方据此按"不认识这条会话"
// 处理,而不是凭空造一条。
func TestLocalSessionID_GivenAnUnknownConversation_ThenReportsZero(t *testing.T) {
	rt := New(newFakeConn(), WithConversationIDResolver(convOf))
	t.Cleanup(func() { _ = rt.Close() })

	assert.Zero(t, rt.localSessionID("018f4c1a-0000-7000-8000-0000000000ff"))
	assert.Zero(t, rt.localSessionID(""))
}

// Given 铸出来(而不是派生出来)的对话身份,When 问它,Then 出线的逐字就是那个值。
// 这是"建档那一刻铸的号原样出现在线格式上"的落点:新对话的号是 UUIDv7,任何按
// (指纹, 会话 id) 现算的方案在它身上都会给出另一个值。
func TestConversationID_GivenAMintedIdentity_ThenReportsItVerbatim(t *testing.T) {
	const minted = "0198f4c1-a000-7c0d-8b21-0000000000ff"
	rt := New(newFakeConn(), WithConversationIDResolver(func(sid int64) string {
		if sid == 42 {
			return minted
		}
		return ""
	}))
	t.Cleanup(func() { _ = rt.Close() })

	assert.Equal(t, minted, rt.conversationID(42))
	assert.NotEqual(t, conversationid.Derive(conversationid.Namespace, "", "42"), minted,
		"这个用例只有在铸出来的值与派生值不同的时候才证明了什么")
	assert.Equal(t, int64(42), rt.localSessionID(minted), "反向也必须落回同一条会话")
}

// Given 注入的翻译说不出这条会话的身份(行已被软删),When 问它,Then 交回空串 ——
// 调用方据此不发这一帧,而不是编一个指向不存在对话的身份发上线。
func TestConversationID_GivenTheResolverKnowsNothing_ThenReportsEmpty(t *testing.T) {
	rt := New(newFakeConn(), WithConversationIDResolver(func(int64) string { return "" }))
	t.Cleanup(func() { _ = rt.Close() })

	assert.Empty(t, rt.conversationID(42))
}
