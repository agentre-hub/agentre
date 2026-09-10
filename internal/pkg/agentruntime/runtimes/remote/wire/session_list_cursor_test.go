package wire_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// TestSessionListCursor_RoundTrips 覆盖游标的往返:调用方原样把它送回来，两端
// 必须解出同一个位置。空串是「从头开始」，不是错误 —— 第一页就是这么发的。
func TestSessionListCursor_RoundTrips(t *testing.T) {
	offset, err := wire.DecodeSessionListCursor("")
	require.NoError(t, err)
	assert.Zero(t, offset, "空游标 = 从最新那条开始")

	offset, err = wire.DecodeSessionListCursor(wire.EncodeSessionListCursor(40))
	require.NoError(t, err)
	assert.Equal(t, 40, offset)
}

// TestSessionListCursor_RejectsGarbage 覆盖坏游标:它是调用方参数错了，得当场说
// 出来。默默当成 0 会让翻到一半的弹层悄悄跳回第一页，读起来像重复的会话。
func TestSessionListCursor_RejectsGarbage(t *testing.T) {
	for _, bad := range []string{"abc", "-1", "1.5", " 4"} {
		_, err := wire.DecodeSessionListCursor(bad)
		assert.Error(t, err, "游标 %q 不该被当成合法位置", bad)
	}
}

// TestClampSessionListLimit 覆盖页大小的收口:0 及以下是协议里「不分页」那一档
// （老客户端不带 limit），照原样传下去；过大的值收到上限，免得一个手写的
// limit=100000 把分页这件事整个绕过去。
func TestClampSessionListLimit(t *testing.T) {
	assert.Equal(t, 0, wire.ClampSessionListLimit(0))
	assert.Equal(t, 0, wire.ClampSessionListLimit(-3), "负数与 0 同义:不分页")
	assert.Equal(t, 20, wire.ClampSessionListLimit(20))
	assert.Equal(t, wire.SessionListMaxLimit, wire.ClampSessionListLimit(wire.SessionListMaxLimit+1))
}
