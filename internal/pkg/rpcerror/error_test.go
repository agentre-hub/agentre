package rpcerror_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

func TestError_GivenWrappedStableError_WhenClassified_ThenPreservesCodeMessageAndDetails(t *testing.T) {
	want := &rpcerror.Error{Code: rpcerror.CodeMethodNotFound, Message: "method not found", Details: []byte{1, 2}}
	err := fmt.Errorf("call failed: %w", want)

	var got *rpcerror.Error
	require.True(t, errors.As(err, &got))
	require.Same(t, want, got)
	require.Equal(t, "method not found", got.Error())
}

func TestError_GivenStandardCodes_WhenUsedAcrossTransports_ThenValuesStayStable(t *testing.T) {
	require.Equal(t, int32(-32601), rpcerror.CodeMethodNotFound)
	require.Equal(t, int32(-32602), rpcerror.CodeInvalidParams)
	require.Equal(t, int32(-32603), rpcerror.CodeInternal)
	require.Equal(t, int32(-32800), rpcerror.CodeCanceled)
}
