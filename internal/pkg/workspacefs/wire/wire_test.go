package wire_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

func TestSentinelRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"PathRefused", wire.ErrPathRefused, wire.ErrCodePathRefused},
		{"BaselineRequired", wire.ErrBaselineRequired, wire.ErrCodeBaselineRequired},
		{"NoCwd", wire.ErrNoCwd, wire.ErrCodeNoCwd},
		{"NotFound", wire.ErrNotFound, wire.ErrCodeNotFound},
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
// 数字分支而不按 message 文本。改一个就是协议破坏。
func TestErrCodes_Stable(t *testing.T) {
	assert.EqualValues(t, -32040, wire.ErrCodePathRefused)
	assert.EqualValues(t, -32041, wire.ErrCodeBaselineRequired)
	assert.EqualValues(t, -32042, wire.ErrCodeNoCwd)
	assert.EqualValues(t, -32043, wire.ErrCodeNotFound)
}

func TestToRPCError_NonSentinel(t *testing.T) {
	assert.Nil(t, wire.ToRPCError(errors.New("random")))
}

func TestFromRPCError_UnknownCode(t *testing.T) {
	src := &rpcerror.Error{Code: -9999, Message: "x"}
	got := wire.FromRPCError(src)
	assert.Equal(t, src, got)
}

// 撞号守卫不在这里 —— 它搬到了 pkg/wire/rpcerror/segments_test.go。
//
// 原来这里有一条 TestErrorCodes_DoNotOverlapRemotefs,把本族的码喂给 remotefs 的
// 翻译器看会不会被认领。它只对得上 remotefs 一族:agentruntime 与 project 的码
// 住在别的包里,这条守卫从来看不见它们。码全部搬进共享 module 之后,那边一条
// AST 守卫扫得到整条协议的每一个码,再在这里留一份窄的副本只会让人以为撞号已经
// 被看住了。
