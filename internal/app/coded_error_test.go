package app

import (
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
)

// 业务码要过得了 wails 那座桥（规格 2026-08-22 D 段）。
//
// wails 把 error 序列化成 err.Error()，而 cago 的 httputils.Error.Error() 只返回
// Msg —— 码在过桥时被丢掉了。前端因此只剩一句本地化文本，分不出「不让看」与
// 「不存在」，而这两件事的出路完全不同（换个目录 vs 去那台机器上放开权限）。
func TestCodedError_CarriesTheCode(t *testing.T) {
	err := codedError(&httputils.Error{Code: 20601, Msg: "远端权限不足"})
	assert.Equal(t, "agentre-code:20601 远端权限不足", err.Error())
}

// 没有码的照原样过 —— 编一个码比不给码更糟。
func TestCodedError_PassesThroughPlainError(t *testing.T) {
	plain := errors.New("dial tcp: connection refused")
	assert.Equal(t, plain, codedError(plain))
}

func TestCodedError_NilStaysNil(t *testing.T) {
	assert.NoError(t, codedError(nil))
}

// 码为 0 等于没有码，不摆一个 `agentre-code:0` 出来让前端去猜。
func TestCodedError_ZeroCodeIsNoCode(t *testing.T) {
	err := codedError(&httputils.Error{Code: 0, Msg: "boom"})
	assert.Equal(t, "boom", err.Error())
}

// 包过一层的也认得出来 —— 判据是 errors.As，不是类型断言。
func TestCodedError_UnwrapsWrapped(t *testing.T) {
	wrapped := errWrap{inner: &httputils.Error{Code: 20602, Msg: "远端路径不存在"}}
	assert.Equal(t, "agentre-code:20602 远端路径不存在", codedError(wrapped).Error())
}

type errWrap struct{ inner error }

func (e errWrap) Error() string { return "wrapped: " + e.inner.Error() }
func (e errWrap) Unwrap() error { return e.inner }
