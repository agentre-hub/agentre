package wire_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
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

func TestToRPCError_NonSentinel(t *testing.T) {
	assert.Nil(t, wire.ToRPCError(errors.New("random")))
}

func TestFromRPCError_UnknownCode(t *testing.T) {
	src := &rpcerror.Error{Code: -9999, Message: "x"}
	got := wire.FromRPCError(src)
	assert.Equal(t, src, got)
}
