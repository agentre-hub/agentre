package wire_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	remotefswire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
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

func TestToRPCError_NonSentinel(t *testing.T) {
	assert.Nil(t, wire.ToRPCError(errors.New("random")))
}

func TestFromRPCError_UnknownCode(t *testing.T) {
	src := &rpcerror.Error{Code: -9999, Message: "x"}
	got := wire.FromRPCError(src)
	assert.Equal(t, src, got)
}

// TestErrorCodes_DoNotOverlapRemotefs 锁死设计决策 5 的一个具体后果:
// workspacefs.* 与 remotefs.* 是独立方法族,各自的 error code 段不能重叠——
// 重叠会让同一个稳定 RPC error code 在两个协议里代表不同语义,客户端按
// FromRPCError rehydrate 时就会翻错 sentinel。
//
// 判定方式是把 workspacefs 的 code 真喂给 remotefs 的翻译器,而不是照抄一份
// remotefs code 常量表来比对:抄下来的表不会随 remotefs 新增 code 更新,那样
// 这条守卫就只在写它的那天成立。remotefs 认不出来时按约定原样返回,拿到的还是
// 同一个 *rpcerror.Error;一旦翻出了别的 sentinel,就是撞号了。
func TestErrorCodes_DoNotOverlapRemotefs(t *testing.T) {
	for _, code := range []int{wire.ErrCodePathRefused, wire.ErrCodeBaselineRequired, wire.ErrCodeNoCwd} {
		src := &rpcerror.Error{Code: int32(code), Message: "x"}
		assert.Samef(t, src, remotefswire.FromRPCError(src),
			"workspacefs code %d 被 remotefs.* 翻成了它自己的 sentinel,两个方法族撞号", code)
	}
	// 反向同理:remotefs 的 code 不能被 workspacefs 认领。
	for _, code := range []int{
		remotefswire.ErrCodePathRefused, remotefswire.ErrCodePermDenied,
		remotefswire.ErrCodeNotFound, remotefswire.ErrCodeNotDir,
		remotefswire.ErrCodeMkdirExists, remotefswire.ErrCodeInvalidName,
	} {
		src := &rpcerror.Error{Code: int32(code), Message: "x"}
		assert.Samef(t, src, wire.FromRPCError(src),
			"remotefs code %d 被 workspacefs.* 翻成了它自己的 sentinel,两个方法族撞号", code)
	}
}
