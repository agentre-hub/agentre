package wire_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// TestSentinelsRoundTripThroughTypedCodes:每个 sentinel 在 wire 上都有稳定的码,
// 且包装过一层(fmt.Errorf %w)仍认得出 —— handler 常要在 sentinel 后面缀上原因。
func TestSentinelsRoundTripThroughTypedCodes(t *testing.T) {
	for _, sentinel := range []error{wire.ErrBackendUnavailable, wire.ErrTranscriptOpen, wire.ErrSessionInUse} {
		mapped := wire.ToRPCError(fmt.Errorf("%w: 细节", sentinel))
		require.NotNil(t, mapped, "sentinel 必须有稳定 wire 码,否则远端只剩一个笼统 internal")
		assert.ErrorIs(t, wire.FromRPCError(mapped), sentinel)
	}
}

// TestErrorCodesDoNotOverlapOtherFamilies:错误码是稳定 wire 值,与既有方法族的
// 码段不重叠(agentruntime remote -32010..-32014、remotefs -32030..-32035、
// workspacefs -32040..-32042)。撞号会让两个方法族的错误在客户端互相翻译成对方。
func TestErrorCodesDoNotOverlapOtherFamilies(t *testing.T) {
	for _, code := range []int32{wire.ErrCodeBackendUnavailable, wire.ErrCodeTranscriptOpen, wire.ErrCodeSessionInUse} {
		assert.Less(t, code, int32(-32042), "落在本族自己的 -3205x 段里")
		assert.GreaterOrEqual(t, code, int32(-32059))
	}
}

// TestMethodNotFoundIsNotSwallowed 是硬约束 3 的判据:-32601 必须原样传上去,由
// 上层翻成 unsupported。在这里被抹成「后端不可用」的话,旧 daemon 就与「这台机器
// 没有会话」再也分不开了。
func TestMethodNotFoundIsNotSwallowed(t *testing.T) {
	notFound := &rpcerror.Error{Code: rpcerror.CodeMethodNotFound, Message: "Method not found"}

	got := wire.FromRPCError(notFound)

	assert.False(t, errors.Is(got, wire.ErrBackendUnavailable))
	assert.False(t, errors.Is(got, wire.ErrTranscriptOpen))
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, got, &rpcErr)
	assert.Equal(t, rpcerror.CodeMethodNotFound, rpcErr.Code)
}
