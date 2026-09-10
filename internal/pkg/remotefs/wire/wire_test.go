package wire_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

func TestSentinelRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"PathRefused", wire.ErrPathRefused, wire.ErrCodePathRefused},
		{"PermDenied", wire.ErrPermDenied, wire.ErrCodePermDenied},
		{"NotFound", wire.ErrNotFound, wire.ErrCodeNotFound},
		{"NotDir", wire.ErrNotDir, wire.ErrCodeNotDir},
		{"MkdirExists", wire.ErrMkdirExists, wire.ErrCodeMkdirExists},
		{"InvalidName", wire.ErrInvalidName, wire.ErrCodeInvalidName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rpcErr := wire.ToRPCError(c.err)
			assert.NotNil(t, rpcErr)
			assert.EqualValues(t, c.code, rpcErr.Code)

			back := wire.FromRPCError(rpcErr)
			assert.True(t, errors.Is(back, c.err))
		})
	}
}

// TestErrCodes_Stable 钉死本族错误码的数值 —— 它们是过线的稳定协议值,对端按
// 数字分支而不按 message 文本。改一个就是协议破坏:已发布的 agentred 与桌面端
// 必须同步升级才不会把别人的失败认成自己的。
func TestErrCodes_Stable(t *testing.T) {
	assert.EqualValues(t, -32030, wire.ErrCodePathRefused)
	assert.EqualValues(t, -32031, wire.ErrCodePermDenied)
	assert.EqualValues(t, -32032, wire.ErrCodeNotFound)
	assert.EqualValues(t, -32033, wire.ErrCodeNotDir)
	assert.EqualValues(t, -32034, wire.ErrCodeMkdirExists)
	assert.EqualValues(t, -32035, wire.ErrCodeInvalidName)
}

func TestToRPCError_NonSentinel(t *testing.T) {
	assert.Nil(t, wire.ToRPCError(errors.New("random")))
}

func TestFromRPCError_UnknownCode(t *testing.T) {
	src := &rpcerror.Error{Code: -9999, Message: "x"}
	got := wire.FromRPCError(src)
	assert.Equal(t, src, got)
}
