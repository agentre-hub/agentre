package remote

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// convOf 是测试里「这条本地会话在线上叫什么」的那一个值 —— 与生产的
// Runtime.conversationID 走同一条派生(测试替身连接没有握手,本端指纹为空)。
// 写死一份等价实现是有意的:用例因此断言的是**约定的值**,而不是"调用方与被调用方
// 用了同一个函数"这种恒真命题。
func convOf(sid int64) string {
	return conversationid.Derive(conversationid.Namespace, "", strconv.FormatInt(sid, 10))
}

// Given 一条本进程从没登记过的会话号,When 问它的对话身份,Then 得到按
// (本端指纹, 会话号) 派生出来的那个 uuid,且反向翻得回同一个会话号。
//
// 这条派生**不是过渡占位值**:它与日后迁移回填对同一批行算出的值同输入同算法,
// 因此逐位相同。改掉其中任何一头,存量对话在两边会得到两个不同的身份。
func TestConversationID_GivenAnUnknownSession_ThenDerivesFromTheOriginatingPeerAndSessionID(t *testing.T) {
	rt := New(newFakeConn())
	t.Cleanup(func() { _ = rt.Close() })

	got := rt.conversationID(42)
	assert.Equal(t, conversationid.Derive(conversationid.Namespace, "", "42"), got)
	require.NoError(t, conversationid.Validate(got))
	assert.Equal(t, int64(42), rt.localSessionID(got))
}

// Given 未知的对话身份,When 反向翻译,Then 交回 0 —— 调用方据此按"不认识这条会话"
// 处理,而不是凭空造一条。
func TestLocalSessionID_GivenAnUnknownConversation_ThenReportsZero(t *testing.T) {
	rt := New(newFakeConn())
	t.Cleanup(func() { _ = rt.Close() })

	assert.Zero(t, rt.localSessionID("018f4c1a-0000-7000-8000-0000000000ff"))
	assert.Zero(t, rt.localSessionID(""))
}
