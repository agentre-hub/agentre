package wire_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
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

// TestErrorCodesFollowTheirOwner:本包的短名字必须与 pkg/wire/rpcerror 里那三个
// 常量同值。
//
// 这里**不自己判断段位**:手抄的段表只在写它的那天成立。段位守卫在
// pkg/wire/rpcerror/segments_test.go,它 AST 扫全部 Code* 常量比对全部段位。
//
// 留在这里的这一条只管一件事:别名不许漂。
func TestErrorCodesFollowTheirOwner(t *testing.T) {
	assert.Equal(t, rpcerror.CodeTranscriptImportBackendUnavailable, wire.ErrCodeBackendUnavailable)
	assert.Equal(t, rpcerror.CodeTranscriptImportTranscriptOpen, wire.ErrCodeTranscriptOpen)
	assert.Equal(t, rpcerror.CodeTranscriptImportSessionInUse, wire.ErrCodeSessionInUse)
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
